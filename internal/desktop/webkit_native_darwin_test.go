//go:build integration && darwin

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Cross-toolkit task through one production connection: AppKit -> WebKit ->
// AppKit. Only synthetic windows receive input; there is no model or JS input.
func TestNativeMacWebKitTransfer(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after native setup; opens synthetic AppKit/WebKit windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	appKit := buildNativeFixture(t, ctx)
	webKit := buildNativeWebKitFixture(t, ctx)
	targets := []nativeFixture{
		startNativeFixture(t, ctx, appKit, "TransferSource", false),
		startNativeFixture(t, ctx, webKit, "TransferWeb", false),
		startNativeFixture(t, ctx, appKit, "TransferDestination", false),
	}
	sentinel := startNativeFixture(t, ctx, appKit, "TransferSentinel", true)
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
	defer func() {
		previous := readNativeState(t, sentinel)
		state := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > previous.Ticks+3 })
		if !state.Active || state.FocusLosses != 0 || state.Value != "AICE-314" || state.Commits != 0 {
			t.Errorf("transfer disturbed sentinel: active=%v focus_losses=%d front_pid=%d", state.Active, state.FocusLosses, state.FrontPID)
		}
	}()
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
		if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
			t.Error("transfer cleanup left shared service unavailable", err)
		}
	}()
	dial, dials := manager.dial, 0
	calls := make(map[string]int)
	manager.dial = func(ctx context.Context) (driverClient, error) {
		client, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		dials++
		return &nativeCountedClient{driverClient: client, calls: calls}, nil
	}
	run, err := manager.Bind(ctx, RunOptions{Mode: BackgroundOnly, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run.Close(); err != nil {
			t.Error(err)
		}
	}()
	discovery, err := run.Windows(ctx, targets[0].prefix, 16)
	if err != nil {
		t.Fatal(err)
	}
	value := "AICE-314"
	started := time.Now()
	for i, target := range targets {
		var ref string
		for _, window := range discovery.Windows {
			if window.PID == target.pid && window.Title == target.name {
				if ref != "" {
					t.Fatal("ambiguous synthetic transfer target")
				}
				ref = window.Ref
			}
		}
		if ref == "" {
			t.Fatalf("exact transfer target %d missing", i)
		}
		obs, err := run.Observe(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
		if err != nil || obs.Image == nil || run.observations[obs.Ref].capture == "" {
			t.Fatalf("transfer target %d capture unavailable: %v", i, err)
		}
		value += fmt.Sprintf(" · stage %d 中文 ✓", i+1)
		kind := "set_value"
		if i == 1 {
			// The pinned Driver documents AXValue writes as unreliable for
			// WebKit. Insert into the initially empty web field instead.
			kind = "type_text"
		}
		set, err := run.Act(ctx, ActRequest{Kind: kind, ObservationRef: obs.Ref,
			ElementToken: nativeTransferElement(t, obs, "Task value", "AXTextField"), Text: value, Screenshot: true})
		nativeReturned(t, set, err)
		var facts struct{ Code, Effect, Path string }
		_ = json.Unmarshal(set.Driver, &facts)
		t.Logf("stage=%d action=%s code=%s effect=%s path=%s timing=%+v", i, kind, facts.Code, facts.Effect, facts.Path, set.Timing)
		if set.Observation.Image == nil || run.observations[set.Observation.Ref].capture == "" {
			t.Fatal("transfer input did not return a verified image")
		}
		click, err := run.Act(ctx, ActRequest{Kind: "click", ObservationRef: set.Observation.Ref,
			ElementToken: nativeTransferElement(t, *set.Observation, "Commit", "AXButton"), Screenshot: true})
		nativeReturned(t, click, err)
		t.Logf("stage=%d action=click timing=%+v", i, click.Timing)
		if click.Observation.Image == nil || run.observations[click.Observation.Ref].capture == "" {
			t.Fatal("transfer click did not return a verified image")
		}
		after := readNativeState(t, target)
		confirmed := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > after.Ticks+3 })
		if confirmed.Value != value || confirmed.Result != "Result: "+value || confirmed.Commits != 1 || semanticCondition(*click.Observation, value) != "satisfied" {
			t.Fatalf("transfer stage %d failed independent input/commit readback: value_matches=%v result_matches=%v commits=%d", i, confirmed.Value == value, confirmed.Result == "Result: "+value, confirmed.Commits)
		}
		value = strings.TrimPrefix(confirmed.Result, "Result: ")
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	if dials != 1 || calls["start_session"] != 1 || calls["end_session"] != 1 || calls["list_windows"] != 1 || calls["set_value"] != 2 || calls["type_text"] != 1 || calls["click"] != 3 || calls["get_window_state"] != 9 {
		t.Fatalf("unexpected cross-toolkit dispatches: dials=%d calls=%v", dials, calls)
	}
	t.Logf("driver=%s model=none mode=background_only stages=3 toolkits=2 elapsed=%s", DriverVersion, time.Since(started))
}

func nativeTransferElement(t *testing.T, observation Observation, label, role string) string {
	t.Helper()
	var token string
	var roles []string
	for _, element := range observation.Elements {
		if element.Label != label {
			continue
		}
		roles = append(roles, element.Role)
		if element.Role == role && element.Token != "" {
			if token != "" {
				t.Fatalf("ambiguous synthetic transfer control %q/%s", label, role)
			}
			token = element.Token
		}
	}
	if token == "" {
		t.Fatalf("synthetic transfer control %q/%s missing; matching roles=%v", label, role, roles)
	}
	return token
}

func buildNativeWebKitFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "fixture")
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "swiftc", "-module-cache-path", filepath.Join(directory, "cache"), "-o", binary, "testdata/webkit-fixture.swift")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile synthetic WebKit fixture: %v\n%s", err, output)
	}
	return binary
}

func TestNativeMacWebKitFixtureBuild(t *testing.T) {
	if os.Getenv("AICE_CUA_BUILD_FIXTURE") != "1" {
		t.Skip("set AICE_CUA_BUILD_FIXTURE=1 to compile the synthetic WebKit fixture only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	buildNativeWebKitFixture(t, ctx)
}
