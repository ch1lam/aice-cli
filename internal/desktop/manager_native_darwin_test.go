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

	"github.com/ch1lam/aice-cli/internal/deps"
)

// Explicit opt-in only. Requires an already installed, authorized pinned service.
// No model, installation, permission request, user app input or full-screen capture.
func TestNativeCuaMultiApp(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	targets := make([]nativeFixture, 3)
	for i := range targets {
		targets[i] = startNativeFixture(t, ctx, binary, fmt.Sprintf("Target%d", i), false)
	}
	sentinel := startNativeFixture(t, ctx, binary, "Sentinel", true)
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	initial := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
	m, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	}()
	dials := 0
	dial := m.dial
	calls := make(map[string]int)
	m.dial = func(ctx context.Context) (driverClient, error) {
		c, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		dials++
		return &nativeCountedClient{driverClient: c, calls: calls}, nil
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
	started := time.Now()
	discovery, err := r.Windows(ctx, targets[0].prefix, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("driver=%s model=none mode=background_only cold_discovery=%s", DriverVersion, time.Since(started))
	value := "AICE-314"
	for i, target := range targets {
		var window *Window
		for j := range discovery.Windows {
			candidate := &discovery.Windows[j]
			if candidate.PID == target.pid && candidate.Title == target.name {
				if window != nil {
					t.Fatal("ambiguous synthetic target")
				}
				window = candidate
			}
		}
		if window == nil {
			t.Fatalf("synthetic target %d not discovered", i)
		}
		observation, err := r.Observe(ctx, ObserveRequest{TargetRef: window.Ref, Screenshot: true})
		if err != nil || observation.Image == nil || observation.ImageWidth <= 0 || observation.ImageHeight <= 0 {
			t.Fatalf("target %d capture unavailable: %v", i, err)
		}
		if i == 0 {
			// Read-only status must not replace the action's native snapshot.
			if _, err := Inspect(ctx, driver, endpoint); err != nil {
				t.Fatal(err)
			}
		}
		value += fmt.Sprintf(" · stage %d 中文 ✓", i+1)
		started = time.Now()
		set, err := r.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: observation.Ref,
			ElementToken: nativeElement(t, observation, "Task value"), Text: value, Screenshot: true})
		nativeReturned(t, set, err)
		t.Logf("target=%d action=set_value timing=%+v", i, set.Timing)
		if set.Observation.Image == nil {
			t.Fatal("action did not return its requested image")
		}
		if _, err := r.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: observation.Ref, ElementToken: "stale", Text: "must not execute"}); err == nil {
			t.Fatal("consumed reference accepted")
		}
		click, err := r.Act(ctx, ActRequest{Kind: "click", ObservationRef: set.Observation.Ref,
			ElementToken: nativeElement(t, *set.Observation, "Commit"), Screenshot: true})
		nativeReturned(t, click, err)
		t.Logf("target=%d action=click timing=%+v", i, click.Timing)
		if semanticCondition(*click.Observation, "Result: "+value) != "satisfied" {
			t.Fatal("action observation did not confirm commit")
		}
		confirmed := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool {
			return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
		})
		value = strings.TrimPrefix(confirmed.Result, "Result: ")
		t.Logf("target=%d warm_set_click_and_observe=%s", i, time.Since(started))
		previous := readNativeState(t, sentinel)
		state := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > previous.Ticks })
		if !state.Active || state.FocusLosses != 0 {
			t.Fatal("background actions disturbed sentinel focus")
		}
	}
	if dials != 1 || calls["start_session"] != 1 || calls["list_windows"] != 1 || calls["set_value"] != 3 || calls["click"] != 3 || calls["get_window_state"] != 9 {
		t.Fatalf("unexpected warm-path dispatches: dials=%d calls=%v", dials, calls)
	}
	final := readNativeState(t, sentinel)
	if final.Ticks <= initial.Ticks || final.Value != "AICE-314" || final.Commits != 0 {
		t.Fatal("sentinel stopped or received task input")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if calls["end_session"] != 1 {
		t.Fatal("owned session was not closed exactly once")
	}
	if _, err := r.Windows(ctx, targets[0].prefix, 16); err == nil {
		t.Fatal("closed run accepted discovery")
	}
	if _, err := Inspect(ctx, driver, endpoint); err != nil {
		t.Fatal("closing run stopped shared service", err)
	}
}

// Read-only preparation shared by native macOS gates, before opening fixtures.
func nativeMacSetup(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	installed, err := deps.InstallCua(ctx, deps.DefaultOptions().WithBinDir(t.TempDir()).WithNoInstall(true))
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(home, "Library", "Caches", "cua-driver", "cua-driver.sock")
	report, err := Inspect(ctx, installed.Installation.Binary, endpoint)
	if err != nil || !report.ConnectionVerified || report.Accessibility != PermissionGranted || report.ScreenRecording != PermissionGranted {
		t.Fatalf("native setup must be completed before this test: %+v %v", report, err)
	}
	return installed.Installation.Binary, endpoint
}

type nativeCountedClient struct {
	driverClient
	calls map[string]int
}

func (c *nativeCountedClient) call(ctx context.Context, name string, args any) (Reply, error) {
	c.calls[name]++
	return c.driverClient.call(ctx, name, args)
}

func nativeReturned(t *testing.T, result ActResult, err error) {
	t.Helper()
	if err != nil || !result.Dispatched || result.Outcome != "returned" || result.DriverError || result.Observation == nil || result.ObservationError != "" {
		// Do not copy native response payloads into diagnostic logs.
		t.Fatalf("native action failed: outcome=%s driver_error=%v observation_error=%s err=%v", result.Outcome, result.DriverError, result.ObservationError, err)
	}
}

func nativeElement(t *testing.T, observation Observation, label string) string {
	t.Helper()
	token := ""
	for _, element := range observation.Elements {
		if element.Label == label && element.Token != "" {
			if token != "" {
				t.Fatalf("ambiguous synthetic element %q", label)
			}
			token = element.Token
		}
	}
	if token == "" {
		t.Fatalf("synthetic element %q unavailable", label)
	}
	return token
}

func buildNativeFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "fixture")
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "swiftc", "-module-cache-path", filepath.Join(directory, "cache"), "-o", binary, "testdata/native-fixture.swift")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile synthetic fixture: %v\n%s", err, output)
	}
	return binary
}

// Compilation can be checked without opening any window or accessing Cua.
func TestNativeCuaFixtureBuild(t *testing.T) {
	if os.Getenv("AICE_CUA_BUILD_FIXTURE") != "1" {
		t.Skip("set AICE_CUA_BUILD_FIXTURE=1 to compile the synthetic AppKit fixture only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	buildNativeFixture(t, ctx)
}

// Exercises only the test windows and their lifecycle, without any Cua process,
// permission request, capture of another app or injected keyboard/mouse event.
func TestNativeCuaFixtureLifecycle(t *testing.T) {
	if os.Getenv("AICE_CUA_TEST_FIXTURE") != "1" {
		t.Skip("set AICE_CUA_TEST_FIXTURE=1 to open synthetic fixture windows only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	binary := buildNativeFixture(t, ctx)
	for i := range 3 {
		fixture := startNativeFixture(t, ctx, binary, fmt.Sprintf("Target%d", i), false)
		state := readNativeState(t, fixture)
		if state.Value != "AICE-314" || state.Commits != 0 {
			t.Fatal("fixture initial state is not synthetic")
		}
	}
	sentinel := startNativeFixture(t, ctx, binary, "Sentinel", true)
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	initial := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed })
	final := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks >= initial.Ticks+5 })
	if !final.Active || final.FocusLosses != 0 || final.Value != "AICE-314" {
		t.Fatal("fixture sentinel did not retain initial focus and contents")
	}
}

type nativeFixture struct {
	directory, prefix, name string
	pid                     int
}

type nativeFixtureState struct {
	PID               int    `json:"pid"`
	Active            bool   `json:"active"`
	Visible           bool   `json:"visible"`
	Key               bool   `json:"key"`
	FrontPID          int    `json:"front_pid"`
	FrontIsLogin      bool   `json:"front_is_login"`
	ActivationPolicy  int    `json:"activation_policy"`
	Armed             bool   `json:"armed"`
	FocusLosses       int    `json:"focus_losses"`
	Ticks             int    `json:"ticks"`
	Value             string `json:"value"`
	Result            string `json:"result"`
	Commits           int    `json:"commits"`
	SelectionLocation int    `json:"selection_location"`
	SelectionLength   int    `json:"selection_length"`
}

func startNativeFixture(t *testing.T, ctx context.Context, binary, label string, sentinel bool) nativeFixture {
	t.Helper()
	dir := t.TempDir()
	// All windows share the unique test binary's directory basename for discovery.
	prefix := "AICE Native " + filepath.Base(filepath.Dir(filepath.Dir(binary)))
	name := prefix + " " + label
	app := filepath.Join(dir, label+".app", "Contents")
	if err := os.MkdirAll(filepath.Join(app, "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	fixtureBinary := filepath.Join(app, "MacOS", "fixture")
	if err := os.WriteFile(fixtureBinary, data, 0700); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>fixture</string><key>CFBundleIdentifier</key><string>test.aice.native.%s</string><key>CFBundleName</key><string>%s</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`, strings.ToLower(label), label)
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	mode := "target"
	if sentinel {
		mode = "sentinel"
	}
	cmd := exec.CommandContext(ctx, fixtureBinary, dir, name, mode)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	fixture := nativeFixture{directory: dir, prefix: prefix, name: name, pid: cmd.Process.Pid}
	awaitNativeState(t, ctx, fixture, func(s nativeFixtureState) bool { return s.PID == fixture.pid })
	if sentinel {
		// LaunchServices provides the explicit initial foreground transition.
		// No activation is performed after arming the focus monitor.
		activateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := exec.CommandContext(activateCtx, "/usr/bin/open", "-a", filepath.Dir(app)).Run(); err != nil {
			t.Fatal("activate synthetic sentinel", err)
		}
	}
	return fixture
}

func readNativeState(t *testing.T, fixture nativeFixture) nativeFixtureState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.directory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state nativeFixtureState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func awaitNativeState(t *testing.T, ctx context.Context, fixture nativeFixture, accept func(nativeFixtureState) bool) nativeFixtureState {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var last nativeFixtureState
	for {
		if _, err := os.Stat(filepath.Join(fixture.directory, "state.json")); err == nil {
			state := readNativeState(t, fixture)
			last = state
			if state.FrontIsLogin {
				t.Fatal("native fixture requires an available interactive desktop; loginwindow is foreground (no unlock attempted)")
			}
			if accept(state) {
				return state
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("synthetic fixture did not reach expected state: pid=%d active=%v visible=%v key=%v front_pid=%d activation_policy=%d ticks=%d: %v", last.PID, last.Active, last.Visible, last.Key, last.FrontPID, last.ActivationPolicy, last.Ticks, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
