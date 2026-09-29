//go:build integration && darwin

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Cold native discovery/launch requires a temporary bundle in ~/Applications,
// one of the pinned Driver's scan roots, plus its LaunchServices registration.
// It is separately opted in and cleaned up using the fixture's private quit file.
func TestNativeMacLaunch(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" || os.Getenv("AICE_CUA_NATIVE_LAUNCH") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 and AICE_CUA_NATIVE_LAUNCH=1 to register a temporary synthetic app in ~/Applications")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	target, bundleID := prepareNativeLaunchFixture(t, ctx, binary)
	sentinel := startNativeFixture(t, ctx, binary, "LaunchSentinel", true)
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	}()
	calls := make(map[string]int)
	dial := manager.dial
	manager.dial = func(ctx context.Context) (driverClient, error) {
		client, err := dial(ctx)
		if err != nil {
			return nil, err
		}
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
	discovery, err := run.discoverApps(ctx, bundleID, 16)
	if err != nil || len(discovery.Apps) != 1 || discovery.Apps[0].BundleID != bundleID || discovery.Apps[0].Ref == "" || discovery.Apps[0].Running || len(discovery.Windows) != 0 {
		t.Fatal("unopened fixture was not discovered as one launchable app", err)
	}
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
	request := ActRequest{Kind: "launch", AppRef: discovery.Apps[0].Ref, Screenshot: true}
	started := time.Now()
	result, err := run.actAndObserve(ctx, request)
	var facts struct {
		PID                      int
		BundleID                 string `json:"bundle_id"`
		SelfActivationSuppressed *bool  `json:"self_activation_suppressed"`
	}
	_ = json.Unmarshal(result.Driver, &facts)
	var suppression any = "not reported"
	if facts.SelfActivationSuppressed != nil {
		suppression = *facts.SelfActivationSuppressed
	}
	t.Logf("cold launch: elapsed=%s outcome=%s driver_error=%v windows=%d reported_self_activation_suppressed=%v", time.Since(started), result.Outcome, result.DriverError, len(result.Windows), suppression)
	if repeated, err := run.actAndObserve(ctx, request); err == nil || repeated.Dispatched {
		t.Error("consumed launch reference was accepted")
	}
	if err != nil || !result.Dispatched || result.Outcome != "returned" || result.DriverError || result.ObservationError != "" || facts.PID <= 0 || facts.BundleID != bundleID {
		t.Fatal("launch did not establish the requested app identity", err)
	}
	var selected Window
	for _, window := range result.Windows {
		t.Logf("synthetic launch candidate: title=%q window_id=%d", window.Title, window.WindowID)
		if window.PID != facts.PID {
			t.Fatal("launch included a foreign process window")
		}
		if window.Title == target.name {
			if selected.Ref != "" {
				t.Fatal("ambiguous synthetic launch target")
			}
			selected = window
		}
	}
	if selected.Ref == "" {
		t.Fatal("launch did not return the exact synthetic window")
	}
	observation := result.Observation
	if len(result.Windows) > 1 {
		if observation != nil || calls["get_window_state"] != 0 || result.Diagnostic == "" {
			t.Fatal("launch automatically chose among multiple candidates")
		}
		// An explicit match to the fixture's known title resolves the candidates;
		// production launch must not simply observe the first returned window.
		observed, err := run.observeWindow(ctx, ObserveRequest{TargetRef: selected.Ref, Screenshot: true})
		if err != nil {
			t.Fatal(err)
		}
		observation = &observed
	}
	if observation == nil || observation.TargetRef != selected.Ref || observation.Image == nil {
		t.Fatal("exact launched window capture unavailable")
	}
	target.pid = facts.PID
	state := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.PID == target.pid })
	markers, err := filepath.Glob(filepath.Join(target.directory, "launched-*"))
	if err != nil || len(markers) != 1 || filepath.Base(markers[0]) != "launched-"+strconv.Itoa(target.pid) || calls["launch_app"] != 1 {
		t.Fatal("launch was replayed or its process identity mismatched", err)
	}
	if state.Commits != 0 || state.Value != "AICE-314" {
		t.Fatal("launch changed initial fixture controls")
	}
	checkFocus := func(phase string) {
		t.Helper()
		after := readNativeState(t, sentinel)
		settled := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > after.Ticks+3 })
		if !settled.Active || settled.FocusLosses != 0 || settled.Value != "AICE-314" || settled.Commits != 0 {
			t.Errorf("%s disturbed foreground sentinel: active=%v focus_losses=%d front_pid=%d", phase, settled.Active, settled.FocusLosses, settled.FrontPID)
		}
	}
	checkFocus("cold launch")
	value := "Launched 中文 ✓"
	set, err := run.actAndObserve(ctx, ActRequest{Kind: "set_value", ObservationRef: observation.Ref, ElementToken: nativeElement(t, *observation, "Task value"), Text: value, Screenshot: true})
	nativeReturned(t, set, err)
	click, err := run.actAndObserve(ctx, ActRequest{Kind: "click", ObservationRef: set.Observation.Ref, ElementToken: nativeElement(t, *set.Observation, "Commit"), Screenshot: true})
	nativeReturned(t, click, err)
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool {
		return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
	})
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	afterClose := readNativeState(t, target)
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterClose.Ticks+3 && s.Commits == 1 })
	checkFocus("launched task and connection cleanup")
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("Manager cleanup stopped shared service", err)
	}
	if calls["launch_app"] != 1 || calls["start_session"] != 1 || calls["end_session"] != 1 {
		t.Fatal("launch/session ownership changed")
	}
	t.Log("one cold launch, exact-window Unicode commit, launched app survived Manager close, shared service preserved")
}

const nativeLaunchRegister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

func prepareNativeLaunchFixture(t *testing.T, ctx context.Context, binary string) (nativeFixture, string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	applications := filepath.Join(home, "Applications")
	if err := os.MkdirAll(applications, 0700); err != nil {
		t.Fatal(err)
	}
	bundle, err := os.MkdirTemp(applications, "AICE Native Launch-*.app")
	if err != nil {
		t.Fatal(err)
	}
	target := nativeFixture{directory: t.TempDir(), name: strings.TrimSuffix(filepath.Base(bundle), ".app")}
	bundleID := "test.aice.native.launch." + strings.TrimPrefix(target.name, "AICE Native Launch-")
	t.Cleanup(func() {
		// Do not kill by PID or bundle name. Only this unique fixture directory
		// controls termination, including when the launch response was lost.
		if err := os.WriteFile(filepath.Join(target.directory, "quit"), nil, 0600); err != nil {
			t.Error("signal launched fixture cleanup", err)
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			markers, err := filepath.Glob(filepath.Join(target.directory, "launched-*"))
			if err != nil {
				t.Error(err)
				return
			}
			allTerminated := true
			for _, marker := range markers {
				pid := strings.TrimPrefix(filepath.Base(marker), "launched-")
				if _, err := os.Stat(filepath.Join(target.directory, "terminated-"+pid)); err != nil {
					allTerminated = false
				}
			}
			if allTerminated {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("fixture did not acknowledge termination; retained bundle %s", bundle)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(cleanupCtx, nativeLaunchRegister, "-u", bundle).Run(); err != nil {
			t.Error("unregister temporary app", err)
		}
		if err := os.RemoveAll(bundle); err != nil {
			t.Error("remove temporary app", err)
		}
	})
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", "fixture"), data, 0700); err != nil {
		t.Fatal(err)
	}
	escape := func(s string) string {
		var b bytes.Buffer
		if err := xml.EscapeText(&b, []byte(s)); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>fixture</string><key>CFBundleIdentifier</key><string>%s</string><key>CFBundleName</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string><key>AICEFixtureDirectory</key><string>%s</string><key>AICEFixtureName</key><string>%s</string></dict></plist>`, escape(bundleID), escape(target.name), escape(target.directory), escape(target.name))
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(ctx, nativeLaunchRegister, "-f", bundle).CombinedOutput(); err != nil {
		t.Fatalf("register temporary app: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(target.directory, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture unexpectedly ran during registration", err)
	}
	return target, bundleID
}
