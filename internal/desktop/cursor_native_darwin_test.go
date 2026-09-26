//go:build integration && darwin

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Uses the official read-only operator CLI for render acknowledgement. This is
// distinct from screenshot/appearance QA and adds no model-visible capability.
func TestNativeMacCursorLifecycle(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after native setup; opens synthetic windows")
	}
	inspection := os.Getenv("AICE_CUA_NATIVE_CURSOR_HOLD_DIR")
	timeout := 2 * time.Minute
	if inspection != "" {
		entries, err := os.ReadDir(inspection)
		if err != nil || !filepath.IsAbs(inspection) || len(entries) != 0 {
			t.Fatal("cursor inspection requires a fresh empty absolute directory", err)
		}
		timeout = 9 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	target := startNativeFixture(t, ctx, binary, "CursorTarget", false)
	sentinel := startNativeFixture(t, ctx, binary, "CursorSentinel", true)
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
	if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
	manager, err := NewManager(func(context.Context) (string, string, error) { return driver, endpoint, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
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
	// The pinned operator view truncates ASCII public labels to 27 characters
	// plus an ellipsis. Require one exact match; never print other sessions.
	label := run.id
	if len(label) > 28 {
		label = label[:27] + "…"
	}
	if found, _ := nativeCursorSession(t, ctx, driver, endpoint, label); found {
		t.Fatal("unstarted binding unexpectedly owns a native session")
	}
	discovery, err := run.Windows(ctx, target.name, 16)
	if err != nil {
		t.Fatal(err)
	}
	ref := ""
	for _, window := range discovery.Windows {
		if window.PID == target.pid && window.Title == target.name {
			if ref != "" {
				t.Fatal("ambiguous cursor test target")
			}
			ref = window.Ref
		}
	}
	if ref == "" {
		t.Fatal("exact cursor test window missing")
	}
	obs, err := run.Observe(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
	if err != nil || obs.Image == nil {
		t.Fatal("cursor test capture unavailable", err)
	}
	if found, visible := nativeCursorSession(t, ctx, driver, endpoint, label); !found || visible {
		t.Fatal("observation did not establish a session with an initially hidden cursor")
	}
	// Bind an external observer before clicking so its setup delay cannot
	// consume the cursor's finite idle display interval.
	nativeCursorInspectionGate(t, ctx, inspection, "ready", "act", filepath.Join(target.directory, "CursorTarget.app"))
	state := readNativeState(t, target)
	result, err := run.Act(ctx, ActRequest{Kind: "click", ObservationRef: obs.Ref, Point: nativeButtonPoint(t, obs, state), Screenshot: true})
	nativeReturned(t, result, err)
	awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Commits == 1 && s.Result == "Result: AICE-314" })
	nativeAwaitCursorSession(t, ctx, driver, endpoint, label, true, true)
	t.Log("native renderer acknowledges the task cursor as visible after one pixel click")
	nativeCursorInspectionGate(t, ctx, inspection, "acted", "finish", "renderer-visible")
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	nativeAwaitCursorSession(t, ctx, driver, endpoint, label, false, false)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("cursor cleanup stopped shared service", err)
	}
	after := readNativeState(t, sentinel)
	final := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > after.Ticks+3 })
	if !final.Active || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
		t.Fatalf("cursor task disturbed sentinel: active=%v focus_losses=%d front_pid=%d", final.Active, final.FocusLosses, final.FrontPID)
	}
	t.Log("cursor lifecycle: absent before discovery, hidden after observe, visible after click, session absent after close; shared service preserved and focus_losses=0")
}

// A manual inspection is opt-in and bounded. The gate never invents an
// appearance verdict, changes cursor settings or repeats native input.
func nativeCursorInspectionGate(t *testing.T, ctx context.Context, directory, ready, release, value string) {
	t.Helper()
	if directory == "" {
		return
	}
	if !filepath.IsAbs(directory) {
		t.Fatal("cursor inspection directory must be absolute")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		t.Fatal("cursor inspection directory must already exist", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ready), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	holdCtx, stop := context.WithTimeout(ctx, 4*time.Minute)
	defer stop()
	for {
		if _, err := os.Stat(filepath.Join(directory, release)); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-holdCtx.Done():
			t.Fatal("external cursor inspection did not release phase", ready)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func nativeCursorSession(t *testing.T, ctx context.Context, driver, endpoint, label string) (found, visible bool) {
	t.Helper()
	data, err := serviceCommand(ctx, driver, "sessions", "list", "--json", "--socket", endpoint)
	if err != nil {
		t.Fatal("read-only operator session inspection failed", err)
	}
	var report struct {
		Sessions []struct {
			Label           *string `json:"session"`
			State           string  `json:"state"`
			CursorVisible   *bool   `json:"cursor_visible"`
			RecordingActive *bool   `json:"recording_active"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(data), &report); err != nil || report.Sessions == nil {
		t.Fatal("invalid operator session report", err)
	}
	for _, s := range report.Sessions {
		if s.Label == nil || *s.Label != label {
			continue
		}
		if found || s.CursorVisible == nil || s.RecordingActive == nil || *s.RecordingActive || (s.State != "active" && s.State != "ending") {
			t.Fatal("ambiguous or invalid test-session cursor state")
		}
		found, visible = true, *s.CursorVisible
	}
	return found, visible
}

func nativeAwaitCursorSession(t *testing.T, ctx context.Context, driver, endpoint, label string, wantFound, wantVisible bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		found, visible := nativeCursorSession(t, ctx, driver, endpoint, label)
		if found == wantFound && visible == wantVisible {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("cursor lifecycle did not settle: found=%v visible=%v want_found=%v want_visible=%v", found, visible, wantFound, wantVisible)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
