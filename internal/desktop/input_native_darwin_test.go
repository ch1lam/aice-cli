//go:build integration && darwin

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"
)

// Separate from set_value acceptance: an RPC success is not input readback.
// Requires explicit setup; only temporary AppKit fixtures receive actions.
func TestNativeMacInput(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	for _, kind := range []string{"type_text_ascii", "type_text_unicode", "key", "hotkey"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			target := startNativeFixture(t, ctx, binary, "InputTarget", false)
			sentinel := startNativeFixture(t, ctx, binary, "InputSentinel", true)
			awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
			if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
			defer func() {
				// Sample after the last Driver response even when input failed.
				previous := readNativeState(t, sentinel)
				final := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > previous.Ticks+3 })
				if !final.Active || !final.Armed || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
					t.Error("native input disturbed foreground sentinel")
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
			discovery, err := run.Windows(ctx, target.name, 16)
			if err != nil {
				t.Fatal(err)
			}
			var ref string
			for _, window := range discovery.Windows {
				if window.PID == target.pid && window.Title == target.name {
					if ref != "" {
						t.Fatal("ambiguous synthetic target")
					}
					ref = window.Ref
				}
			}
			if ref == "" {
				t.Fatal("exact synthetic target missing")
			}
			obs, err := run.Observe(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
			if err != nil || obs.Image == nil || obs.Degraded {
				t.Fatal("initial native observation unavailable", err)
			}
			if kind != "hotkey" {
				seed, err := run.Act(ctx, ActRequest{Kind: "set_value", ObservationRef: obs.Ref, ElementToken: nativeElement(t, obs, "Task value"), Screenshot: true})
				nativeReturned(t, seed, err)
				obs = *seed.Observation
				awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Value == "" })
			}
			request := ActRequest{Kind: kind, ObservationRef: obs.Ref, ElementToken: nativeElement(t, obs, "Task value"), Screenshot: true}
			want := ""
			switch kind {
			case "type_text_ascii":
				request.Kind, request.Text, want = "type_text", "Native ASCII", "Native ASCII"
			case "type_text_unicode":
				request.Kind, request.Text, want = "type_text", "Native 中文 ✓", "Native 中文 ✓"
			case "key":
				request.Key, want = "x", "x"
			case "hotkey":
				request.Keys, want = []string{"cmd", "a"}, "AICE-314"
			}
			started := time.Now()
			result, err := run.Act(ctx, request)
			var facts struct{ Code, Effect string }
			_ = json.Unmarshal(result.Driver, &facts)
			t.Logf("native action=%s elapsed=%s outcome=%s driver_error=%v code=%s effect=%s", kind, time.Since(started), result.Outcome, result.DriverError, facts.Code, facts.Effect)
			if err != nil || !result.Dispatched || result.Outcome != "returned" || result.DriverError || result.Observation == nil || result.Observation.Image == nil || result.ObservationError != "" {
				t.Error("native action or follow-up observation failed", err)
			}
			afterReply := readNativeState(t, target)
			settled := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
			if settled.Value != want || settled.Commits != 0 {
				t.Errorf("native input readback mismatch: expected_bytes=%d expected_runes=%d actual_bytes=%d actual_runes=%d commits=%d", len(want), utf8.RuneCountInString(want), len(settled.Value), utf8.RuneCountInString(settled.Value), settled.Commits)
			}
			if kind == "hotkey" && (settled.SelectionLocation != 0 || settled.SelectionLength != len(want)) {
				t.Error("select-all hotkey did not select the complete synthetic field")
			}
		})
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("native input cleanup stopped shared service", err)
	}
}
