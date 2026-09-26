//go:build integration && linux

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Real XDG discovery/launch and exact-window binding. Never uses a host desktop.
func TestNativeLinuxLaunch(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	home := t.TempDir()
	t.Setenv("HOME", home)
	applications := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(applications, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, "fixture.py")
	if err := os.WriteFile(script, linuxFixtureScript, 0600); err != nil {
		t.Fatal(err)
	}
	target := linuxProbeFixture{directory: t.TempDir(), name: "AICE Native Launch Target"}
	launcher := filepath.Join(home, "aice-native-launch")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// Exec retains the launcher PID. Its marker proves that a consumed app ref
	// does not start the app twice; it also identifies cleanup after a lost reply.
	marker := filepath.Join(target.directory, "launches")
	body := "#!/bin/sh\nprintf '%s\\n' \"$$\" >> " + quote(marker) + "\nexec /usr/bin/python3 " + quote(script) + " " + quote(target.directory) + " " + quote(target.name) + " target\n"
	if err := os.WriteFile(launcher, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	const bundleID = "test.aice.native.launch"
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=AICE Native Launch\nExec=%s\nTerminal=false\n", launcher)
	if err := os.WriteFile(filepath.Join(applications, bundleID+".desktop"), []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	sentinel := startLinuxProbeFixture(t, ctx, script, "AICE Launch Sentinel", "sentinel")
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Active })
	manager, err := NewManager(func(context.Context) (string, string, error) { return binary, filepath.Join(home, "cua.sock"), nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	// This is test-owned cleanup, not Manager ownership of launched apps. Check
	// the unique fixture command before signalling a PID recorded by the wrapper.
	defer func() {
		data, _ := os.ReadFile(marker)
		for _, line := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(line)
			if err != nil || pid <= 1 {
				t.Error("invalid fixture launch marker")
				continue
			}
			cmdline, err := os.ReadFile(filepath.Join("/proc", line, "cmdline"))
			if err == nil && strings.Contains(string(cmdline), script+"\x00"+target.directory+"\x00") {
				if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
					t.Error("clean up launched fixture", err)
				}
			}
		}
	}()
	run, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	discovery, err := run.Apps(ctx, bundleID, 16)
	if err != nil || len(discovery.Apps) != 1 || discovery.Apps[0].Ref == "" || discovery.Apps[0].Running || len(discovery.Windows) != 0 {
		t.Fatal("unopened fixture was not discovered as a launchable app", err)
	}
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.KeysSent >= 3 })
	request := ActRequest{Kind: "launch", AppRef: discovery.Apps[0].Ref, Screenshot: true}
	started := time.Now()
	result, err := run.Act(ctx, request)
	var facts struct {
		PID             int
		Code, Effect    string
		Active, Running bool
	}
	_ = json.Unmarshal(result.Driver, &facts)
	t.Logf("launch elapsed=%s outcome=%s driver_error=%v code=%s reported_active=%v running=%v windows=%d", time.Since(started), result.Outcome, result.DriverError, facts.Code, facts.Active, facts.Running, len(result.Windows))
	if _, err := run.Act(ctx, request); err == nil {
		t.Error("consumed launch reference was accepted")
	}
	if err := os.WriteFile(filepath.Join(sentinel.directory, "stop-typing"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	afterReply := awaitLinuxProbeState(t, ctx, sentinel, func(linuxProbeState) bool { return true })
	settled := awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Ticks > afterReply.Ticks+3 })
	if !settled.Active || settled.FocusLosses != 0 || settled.Value != strings.Repeat("a", settled.KeysSent) {
		t.Errorf("launch disturbed foreground sentinel: active=%v focus_losses=%d keys_sent=%d retained=%d", settled.Active, settled.FocusLosses, settled.KeysSent, len(settled.Value))
	}
	linuxManagerReturned(t, run, result, err)
	if len(result.Windows) != 1 || result.Windows[0].PID != facts.PID || result.Windows[0].Title != target.name {
		t.Fatal("launch did not bind its exact synthetic window")
	}
	data, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(facts.PID) {
		t.Fatal("launcher count or returned process identity mismatched", err)
	}
	value := "Launched 中文 ✓"
	set, err := run.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: result.Observation.Ref,
		ElementToken: (linuxProbeObservation{Elements: result.Observation.Elements}).token(t, "Task value"), Text: value, Screenshot: true})
	linuxManagerReturned(t, run, set, err)
	click, err := run.Act(ctx, ActRequest{Kind: "click", ObservationRef: set.Observation.Ref,
		ElementToken: (linuxProbeObservation{Elements: set.Observation.Elements}).token(t, "Commit"), Screenshot: true})
	linuxManagerReturned(t, run, click, err)
	awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool {
		return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
	})
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	afterClose := awaitLinuxProbeState(t, ctx, target, func(linuxProbeState) bool { return true })
	awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool { return s.Ticks > afterClose.Ticks+3 })
	t.Log("one launch, exact window task completed, launched fixture survived Manager close")
}
