//go:build integration && linux

package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This pin belongs only to the upstream capability probe. It does not admit
// Linux actions through NewManager until the platform adapter is implemented.
//
//go:embed testdata/linux-probe-0.29.1.json
var linuxProbeSchemas []byte

//go:embed testdata/linux-fixture.py
var linuxFixtureScript []byte

// TestNativeLinuxBackgroundProbe establishes upstream capabilities separately
// from AICE Manager acceptance. It runs only inside a disposable Xvfb/GTK/D-Bus
// container with no user display, bus, home or input device mounted.
func TestNativeLinuxBackgroundProbe(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(home, "fixture.py")
	if err := os.WriteFile(script, linuxFixtureScript, 0600); err != nil {
		t.Fatal(err)
	}
	var targets []linuxProbeFixture
	for i := range 3 {
		targets = append(targets, startLinuxProbeFixture(t, ctx, script, fmt.Sprintf("AICE Target %d", i), "target"))
	}
	sentinel := startLinuxProbeFixture(t, ctx, script, "AICE Sentinel", "sentinel")
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Active })
	endpoint := filepath.Join(home, "cua.sock")
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
	report, err := Inspect(ctx, binary, endpoint)
	if err != nil || !report.ConnectionVerified || report.Linux == nil || report.Linux.X11 != PermissionGranted || report.Linux.ATSPI != PermissionGranted {
		t.Fatalf("X11/AT-SPI setup unavailable: %+v %v", report.Linux, err)
	}
	transport, err := newProcessTransport(binary, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	c, err := connectReviewed(ctx, transport, func(actual map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		return reviewedTools(actual, linuxProbeSchemas, "linux")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.close(); err != nil {
			t.Error(err)
		}
	}()
	call := func(name string, args map[string]any) Reply {
		t.Helper()
		reply, err := c.call(ctx, name, args)
		if err != nil || reply.IsError {
			// All window contents in this opt-in test are synthetic. Only return
			// a code here; do not teach production diagnostics to dump payloads.
			var failure struct{ Code, Effect string }
			_ = json.Unmarshal(reply.Structured, &failure)
			t.Fatalf("native %s failed: code=%s effect=%s err=%v", name, failure.Code, failure.Effect, err)
		}
		return reply
	}
	session := "aice-linux-probe"
	call("start_session", map[string]any{"session": session})
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if reply, err := c.call(cleanup, "end_session", map[string]any{"session": session}); err != nil || reply.IsError {
			t.Error("native session cleanup failed", err)
		}
	}()
	var windows struct {
		Windows []struct {
			windowIdentity
			Title string `json:"title"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(call("list_windows", map[string]any{}).Structured, &windows); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Armed && s.KeysSent >= 3 })
	value := "AICE-314"
	for i, target := range targets {
		var identity windowIdentity
		for _, window := range windows.Windows {
			if window.PID == target.pid && window.Title == target.name {
				if identity.PID != 0 {
					t.Fatal("ambiguous native fixture window")
				}
				identity = window.windowIdentity
			}
		}
		if identity.PID == 0 || identity.WindowID == 0 {
			t.Fatalf("target %d not discovered", i)
		}
		observe := func() linuxProbeObservation {
			t.Helper()
			reply := call("get_window_state", map[string]any{"session": session, "pid": identity.PID, "window_id": identity.WindowID,
				"include_screenshot": true, "include_accessibility_tree": true, "max_elements": 200, "max_depth": 15, "max_image_dimension": 1600, "timeout_ms": 5000})
			var state linuxProbeObservation
			if err := json.Unmarshal(reply.Structured, &state); err != nil || state.windowIdentity != identity || state.Snapshot == "" || state.Capture == "" || len(reply.Images) != 1 {
				t.Fatalf("target %d observation identity/image missing: snapshot=%t capture=%t images=%d err=%v", i, state.Snapshot != "", state.Capture != "", len(reply.Images), err)
			}
			picture, format, err := image.DecodeConfig(bytes.NewReader(reply.Images[0].Data))
			if err != nil || format != "png" || picture.Width != state.Width || picture.Height != state.Height {
				t.Fatal("native screenshot dimensions do not match its payload", err)
			}
			return state
		}
		started := time.Now()
		before := observe()
		if i == 0 {
			if _, err := Inspect(ctx, binary, endpoint); err != nil {
				t.Fatal("read-only inspection during task", err)
			}
		}
		value += fmt.Sprintf(" · stage %d 中文 ✓", i+1)
		call("set_value", map[string]any{"session": session, "pid": identity.PID, "window_id": identity.WindowID, "element_token": before.token(t, "Task value"), "value": value})
		after := observe()
		stale, err := c.call(ctx, "set_value", map[string]any{"session": session, "pid": identity.PID, "window_id": identity.WindowID, "element_token": before.token(t, "Task value"), "value": "stale input must not execute"})
		if err != nil || !stale.IsError {
			t.Fatal("superseded native token was not rejected", err)
		}
		call("click", map[string]any{"session": session, "pid": identity.PID, "window_id": identity.WindowID, "element_token": after.token(t, "Commit"), "delivery_mode": "background"})
		observe()
		confirmed := awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool {
			return s.Value == value && s.Result == "Result: "+value && s.Commits == 1
		})
		value = strings.TrimPrefix(confirmed.Result, "Result: ")
		t.Logf("driver=%s target=%d semantic_set_click_three_captures=%s frame_valid_field_present=%t", DriverVersion, i, time.Since(started), before.FrameValid != nil)
	}
	if err := os.WriteFile(filepath.Join(sentinel.directory, "stop-typing"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	final := awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.KeysSent > 3 && s.Value == strings.Repeat("a", s.KeysSent) })
	if !final.Active || final.FocusLosses != 0 || final.Commits != 0 {
		t.Fatalf("background actions disturbed synthetic user: active=%v focus_losses=%d commits=%d", final.Active, final.FocusLosses, final.Commits)
	}
	t.Logf("three GTK targets confirmed; sentinel retained all %d concurrent core keystrokes with zero focus loss", final.KeysSent)
}

// Deliberately transfer focus between test-owned windows to establish that the
// sentinel detects transient focus loss and core keys reaching the wrong app.
// No Cua service or native action is involved in this harness control.
func TestNativeLinuxFocusSentinel(t *testing.T) {
	if os.Getenv("AICE_CUA_X11_CONTAINER") != "1" {
		t.Skip("requires the explicitly isolated Linux X11 fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Getenv("DISPLAY") != ":99" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatal("requires the test container's Xvfb :99 and private session bus")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	directory := t.TempDir()
	script := filepath.Join(directory, "fixture.py")
	if err := os.WriteFile(script, linuxFixtureScript, 0600); err != nil {
		t.Fatal(err)
	}
	target := startLinuxProbeFixture(t, ctx, script, "AICE Focus Control", "target")
	sentinel := startLinuxProbeFixture(t, ctx, script, "AICE Sentinel Control", "sentinel")
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Active })
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.KeysSent >= 3 })
	if err := os.WriteFile(filepath.Join(target.directory, "activate"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return !s.Active && s.FocusLosses > 0 })
	awaitLinuxProbeState(t, ctx, target, func(s linuxProbeState) bool {
		return s.Active && s.Value != "AICE-314" && strings.Contains(s.Value, "a")
	})
	// Restore the sentinel to prove a final-focus-only assertion would miss the
	// interruption; its cumulative loss counter must retain the evidence.
	if err := os.WriteFile(filepath.Join(sentinel.directory, "activate"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitLinuxProbeState(t, ctx, sentinel, func(s linuxProbeState) bool { return s.Active && s.FocusLosses > 0 })
	t.Log("sentinel detected deliberate focus loss and misdirected core input after focus restoration")
}

type linuxProbeObservation struct {
	windowIdentity
	Snapshot   string    `json:"snapshot_id"`
	Capture    string    `json:"capture_id"`
	Elements   []Element `json:"elements"`
	Width      int       `json:"screenshot_width"`
	Height     int       `json:"screenshot_height"`
	FrameValid *bool     `json:"screenshot_frame_valid"`
}

func (s linuxProbeObservation) token(t *testing.T, label string) string {
	t.Helper()
	var token string
	for _, element := range s.Elements {
		if element.Label == label && element.Token != "" {
			if token != "" {
				t.Fatalf("ambiguous element %q", label)
			}
			token = element.Token
		}
	}
	if token == "" {
		t.Fatalf("synthetic element %q unavailable", label)
	}
	return token
}

type linuxProbeFixture struct {
	directory, name string
	pid             int
}

type linuxProbeState struct {
	Active      bool   `json:"active"`
	Armed       bool   `json:"armed"`
	FocusLosses int    `json:"focus_losses"`
	KeysSent    int    `json:"keys_sent"`
	Commits     int    `json:"commits"`
	Value       string `json:"value"`
	Result      string `json:"result"`
}

func startLinuxProbeChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func startLinuxProbeFixture(t *testing.T, ctx context.Context, script, name, mode string) linuxProbeFixture {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", script, dir, name, mode)
	startLinuxProbeChild(t, cmd)
	f := linuxProbeFixture{directory: dir, name: name, pid: cmd.Process.Pid}
	awaitLinuxProbeState(t, ctx, f, func(linuxProbeState) bool { return true })
	return f
}

func awaitLinuxProbeState(t *testing.T, ctx context.Context, f linuxProbeFixture, accept func(linuxProbeState) bool) linuxProbeState {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var state linuxProbeState
	for {
		data, err := os.ReadFile(filepath.Join(f.directory, "state.json"))
		if err == nil {
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if accept(state) {
				return state
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := waitNativeProbe(ctx); err != nil {
			t.Fatalf("fixture %s did not settle: active=%v focus_losses=%d sent=%d received_length=%d commits=%d: %v", f.name, state.Active, state.FocusLosses, state.KeysSent, len(state.Value), state.Commits, err)
		}
	}
}
