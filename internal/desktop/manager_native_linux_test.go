//go:build integration && linux

package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeLinuxManager(t *testing.T) {
	if os.Getenv("AICE_CUA_X11_CONTAINER") != "1" {
		t.Skip("requires the explicitly isolated Linux X11 fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Getenv("DISPLAY") != ":99" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatal("requires the test container's Xvfb :99 and private session bus")
	}
	binary := os.Getenv("AICE_CUA_TEST_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("requires an explicitly supplied checksum-verified Driver")
	}
	for _, mode := range []string{"owned-stdio", "shared-service"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			home := t.TempDir()
			t.Setenv("HOME", home)
			script := filepath.Join(home, "fixture.py")
			if err := os.WriteFile(script, linuxFixtureScript, 0600); err != nil {
				t.Fatal(err)
			}
			var targets []linuxProbeFixture
			for i := range 3 {
				targets = append(targets, startLinuxProbeFixture(t, ctx, script, fmt.Sprintf("AICE Manager Target %d", i), "target"))
			}
			sentinel := startLinuxProbeFixture(t, ctx, script, "AICE Manager Sentinel", "sentinel")
			awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Active })
			endpoint := filepath.Join(home, ".cache", "cua-driver", "cua-driver.sock")
			if mode == "shared-service" {
				daemon := exec.CommandContext(ctx, binary, "serve", "--socket", endpoint, "--permission-mode", "standard", "--no-permissions-gate")
				daemon.Env = driverEnvironment(os.Environ())
				startLinuxProbeChild(t, daemon)
				for {
					if _, err := os.Stat(endpoint); err == nil {
						break
					}
					if err := waitNativeProbe(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := linuxDriverPIDs(t, binary)
			m, err := NewManager(func(context.Context) (string, string, error) { return binary, endpoint, nil })
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := m.Close(); err != nil {
					t.Error(err)
				}
			}()
			dials := 0
			calls := make(map[string]int)
			dial := m.dial
			m.dial = func(ctx context.Context) (driverClient, error) {
				c, err := dial(ctx)
				if err != nil {
					return nil, err
				}
				dials++
				return &linuxCountedClient{driverClient: c, calls: calls}, nil
			}
			r, err := m.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.KeysSent >= 3 })
			started := time.Now()
			discovery, err := r.Windows(ctx, "AICE Manager Target", 16)
			if err != nil || len(discovery.Windows) != 3 {
				t.Fatalf("native manager discovery: count=%d err=%v", len(discovery.Windows), err)
			}
			t.Logf("mode=%s cold_discovery=%s", mode, time.Since(started))
			value := "AICE-314"
			for i, target := range targets {
				var selected Window
				for _, candidate := range discovery.Windows {
					if candidate.PID == target.pid && candidate.Title == target.name {
						selected = candidate
					}
				}
				if selected.Ref == "" {
					t.Fatal("exact synthetic target missing")
				}
				observation, err := r.Observe(ctx, ObserveRequest{TargetRef: selected.Ref, Screenshot: true})
				if err != nil || observation.Image == nil || r.observations[observation.Ref].capture == "" {
					t.Fatal("Linux image did not establish a verified capture mapping", err)
				}
				value += fmt.Sprintf(" · stage %d 中文 ✓", i+1)
				set, err := r.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: observation.Ref,
					ElementToken: (linuxProbeObservation{Elements: observation.Elements}).token(t, "Task value"), Text: value, Screenshot: true})
				linuxManagerReturned(t, r, set, err)
				t.Logf("target=%d action=set_value timing=%+v", i, set.Timing)
				if _, err := r.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: observation.Ref, ElementToken: "stale", Text: "must not execute"}); err == nil {
					t.Fatal("consumed AICE reference was accepted")
				}
				click, err := r.Act(ctx, ActRequest{Kind: "click", ObservationRef: set.Observation.Ref,
					ElementToken: (linuxProbeObservation{Elements: set.Observation.Elements}).token(t, "Commit"), Screenshot: true})
				linuxManagerReturned(t, r, click, err)
				t.Logf("target=%d action=click timing=%+v", i, click.Timing)
				if click.Observation.Complete {
					t.Fatal("actionable-only Linux projection claimed all visible text")
				}
				confirmed := awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool {
					return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
				})
				value = strings.TrimPrefix(confirmed.Result, "Result: ")
			}
			if err := os.WriteFile(filepath.Join(sentinel.directory, "stop-typing"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			final := awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Value == strings.Repeat("a", s.KeysSent) })
			if !final.Active || final.FocusLosses != 0 || final.Commits != 0 || final.KeysSent < 3 {
				t.Fatal("Manager actions disturbed sentinel input or focus")
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if dials != 1 || calls["start_session"] != 1 || calls["end_session"] != 1 || calls["list_windows"] != 1 || calls["get_window_state"] != 9 || calls["set_value"] != 3 || calls["click"] != 3 {
				t.Fatal("warm reuse or cleanup mismatch", dials, calls)
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			setup, err := Setup(ctx, binary, endpoint, SetupOptions{SelectWindow: func(_ context.Context, windows []Window) (string, error) {
				for _, window := range windows {
					if window.PID == targets[0].pid && window.Title == targets[0].name {
						return window.Ref, nil
					}
				}
				return "", fmt.Errorf("setup target missing")
			}})
			if err != nil || !setup.Ready || !setup.ConnectionVerified || !setup.CaptureVerified || setup.AuthorizationRequested || setup.LaunchRequested {
				t.Fatal("native selected-window setup", setup, err)
			}
			for pid := range linuxDriverPIDs(t, binary) {
				if !before[pid] {
					t.Fatalf("owned MCP child %d survived Manager close", pid)
				}
			}
			report, err := Inspect(ctx, binary, endpoint)
			if mode == "shared-service" && (err != nil || !report.ConnectionVerified) {
				t.Fatal("Manager close stopped the shared service", err)
			}
			if mode == "owned-stdio" && !serviceHasCode(err, "not_running") {
				t.Fatal("private runtime published a shared daemon", err)
			}
			t.Logf("mode=%s three targets confirmed, %d concurrent keys retained, one connection/session, owned children reaped", mode, final.KeysSent)
		})
	}
}

func linuxManagerReturned(t *testing.T, run *Run, result ActResult, err error) {
	t.Helper()
	if err != nil || !result.Dispatched || result.Outcome != "returned" || result.DriverError || result.Observation == nil || result.Observation.Image == nil || result.ObservationError != "" {
		t.Fatalf("native Manager action: outcome=%s driver_error=%v observation_error=%s err=%v", result.Outcome, result.DriverError, result.ObservationError, err)
	}
	if run.observations[result.Observation.Ref].capture == "" {
		t.Fatal("post-action image did not establish a verified capture mapping")
	}
}

type linuxCountedClient struct {
	driverClient
	calls map[string]int
}

func (c *linuxCountedClient) call(ctx context.Context, name string, args any) (Reply, error) {
	c.calls[name]++
	return c.driverClient.call(ctx, name, args)
}

func linuxDriverPIDs(t *testing.T, binary string) map[int]bool {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	pids := make(map[int]bool)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if executable, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe")); err == nil && executable == binary {
			pids[pid] = true
		}
	}
	return pids
}
