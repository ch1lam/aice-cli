//go:build integration && darwin

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Actual screenshot-coordinate delivery, independently measured in an AppKit
// fixture. No visual model or user app receives input. A resize must refuse the
// old capture before input; recovery uses a fresh image and newly derived point.
func TestNativeMacPixelClick(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	for _, kind := range []string{"click", "resize"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			target := startNativeFixture(t, ctx, binary, "PixelTarget", false)
			sentinel := startNativeFixture(t, ctx, binary, "PixelSentinel", true)
			awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
			if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
			defer func() {
				after := readNativeState(t, sentinel)
				final := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > after.Ticks+3 })
				if !final.Active || final.FocusLosses != 0 || final.Value != "AICE-314" || final.Commits != 0 {
					t.Errorf("pixel action disturbed sentinel: active=%v focus_losses=%d front_pid=%d", final.Active, final.FocusLosses, final.FrontPID)
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
			if err != nil || obs.Image == nil || obs.Degraded || run.observations[obs.Ref].capture == "" {
				t.Fatal("initial pixel mapping unavailable", err)
			}
			before := readNativeState(t, target)
			point := nativeButtonPoint(t, obs, before)
			t.Logf("case=%s frame_points=%.0fx%.0f image_pixels=%dx%d", kind, before.Width, before.Height, obs.ImageWidth, obs.ImageHeight)
			if kind == "resize" {
				if err := os.WriteFile(filepath.Join(target.directory, "resize"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Width == 900 && s.Height != before.Height })
			}
			result, err := run.Act(ctx, ActRequest{Kind: "click", ObservationRef: obs.Ref, Point: point, Screenshot: true})
			var facts struct{ Code, Effect string }
			_ = json.Unmarshal(result.Driver, &facts)
			t.Logf("case=%s timing=%+v outcome=%s driver_error=%v code=%s effect=%s", kind, result.Timing, result.Outcome, result.DriverError, facts.Code, facts.Effect)
			afterReply := readNativeState(t, target)
			settled := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
			if kind == "resize" {
				if err != nil || !result.Dispatched || result.Outcome != "returned" || !result.DriverError || facts.Code != "capture_frame_mismatch" || facts.Effect != "refused" || settled.Commits != 0 || settled.Value != before.Value {
					t.Fatal("old capture was not refused before input", err)
				}
				fresh := result.Observation
				if fresh == nil || fresh.Image == nil || fresh.Degraded || run.observations[fresh.Ref].capture == "" {
					t.Fatal("resize refusal did not return a usable fresh observation")
				}
				t.Logf("resized frame_points=%.0fx%.0f image_pixels=%dx%d", settled.Width, settled.Height, fresh.ImageWidth, fresh.ImageHeight)
				result, err = run.Act(ctx, ActRequest{Kind: "click", ObservationRef: fresh.Ref, Point: nativeButtonPoint(t, *fresh, settled), Screenshot: true})
				t.Logf("case=resize recovery timing=%+v", result.Timing)
			}
			nativeReturned(t, result, err)
			if result.Observation.Image == nil || run.observations[result.Observation.Ref].capture == "" {
				t.Fatal("pixel click did not return a verified fresh capture")
			}
			afterReply = readNativeState(t, target)
			settled = awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
			if settled.Commits != 1 || settled.Result != "Result: AICE-314" || settled.Value != before.Value {
				t.Fatalf("pixel click postcondition failed: commits=%d result_matches=%v input_unchanged=%v", settled.Commits, settled.Result == "Result: AICE-314", settled.Value == before.Value)
			}
		})
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("pixel test cleanup stopped shared service", err)
	}
}

func nativeButtonPoint(t *testing.T, observation Observation, state nativeFixtureState) *Point {
	t.Helper()
	if state.Width <= 0 || state.Height <= 0 || state.ButtonX <= 0 || state.ButtonY <= 0 || state.ButtonX >= state.Width || state.ButtonY >= state.Height {
		t.Fatal("fixture button geometry unavailable")
	}
	// Convert independently reported AppKit points to the image actually sent
	// to the model. AICE/Driver remain responsible for their own inverse scales.
	return &Point{X: state.ButtonX * float64(observation.ImageWidth) / state.Width, Y: state.ButtonY * float64(observation.ImageHeight) / state.Height}
}
