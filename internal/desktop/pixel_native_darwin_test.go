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
	testNativeMacPixelInput(t, []string{"click", "resize"}, BackgroundOnly)
}

// Uses one display with a partly off-screen window, not a multi-monitor claim.
func TestNativeMacWindowMove(t *testing.T) {
	testNativeMacPixelInput(t, []string{"move-left"}, BackgroundOnly)
}

func TestNativeMacPointerButtons(t *testing.T) {
	testNativeMacPixelInput(t, []string{"double_click", "right_click"}, BackgroundOnly)
}

// Refusal remains a failing input postcondition, not an expected-pass case.
func TestNativeMacGestures(t *testing.T) {
	testNativeMacPixelInput(t, []string{"scroll", "drag"}, BackgroundOnly)
}

// Separate opt-in: the synthetic target may receive foreground input. This
// never changes the user's saved preference or makes background refusal pass.
func TestNativeMacForegroundDrag(t *testing.T) {
	if os.Getenv("AICE_CUA_NATIVE_FOREGROUND") != "1" {
		t.Skip("set AICE_CUA_NATIVE_FOREGROUND=1 to allow foreground input to a synthetic slider")
	}
	testNativeMacPixelInput(t, []string{"drag"}, ForegroundAllowed)
}

func testNativeMacPixelInput(t *testing.T, kinds []string, controlMode ControlMode) {
	t.Helper()
	if os.Getenv("AICE_CUA_NATIVE") != "1" {
		t.Skip("set AICE_CUA_NATIVE=1 after explicit native setup; opens synthetic windows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	driver, endpoint := nativeMacSetup(t, ctx)
	binary := buildNativeFixture(t, ctx)
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			mode := "target"
			if kind == "scroll" || kind == "drag" {
				mode = "gestures"
			} else if kind == "double_click" || kind == "right_click" {
				mode = "pointer"
			}
			target := startNativeFixtureMode(t, ctx, binary, "PixelTarget", mode)
			sentinel := startNativeFixture(t, ctx, binary, "PixelSentinel", true)
			awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Active })
			if err := os.WriteFile(filepath.Join(sentinel.directory, "arm"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Armed && s.Active })
			defer func() {
				after := readNativeState(t, sentinel)
				final := awaitNativeState(t, ctx, sentinel, func(s nativeFixtureState) bool { return s.Ticks > after.Ticks+3 })
				if !final.Active || (controlMode == BackgroundOnly && final.FocusLosses != 0) || final.Value != "AICE-314" || final.Commits != 0 {
					t.Errorf("pixel action disturbed sentinel: active=%v focus_losses=%d front_pid=%d", final.Active, final.FocusLosses, final.FrontPID)
				}
				t.Logf("sentinel mode=%s restored=%v focus_losses=%d", controlMode, final.Active, final.FocusLosses)
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
			calls := make(map[string]int)
			dial := manager.dial
			manager.dial = func(ctx context.Context) (driverClient, error) {
				client, err := dial(ctx)
				if err != nil {
					return nil, err
				}
				return &nativeCountedClient{driverClient: client, calls: calls}, nil
			}
			run, err := bindFixtureRun(manager, ctx, RunOptions{Mode: controlMode, Images: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := run.Close(); err != nil {
					t.Error(err)
				}
			}()
			discovery, err := run.discoverWindows(ctx, target.name, 16)
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
			obs, err := run.observeWindow(ctx, ObserveRequest{TargetRef: ref, Screenshot: true})
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
			if kind == "move-left" {
				if before.FrameX < 0 {
					t.Fatal("fixture did not start at a nonnegative screen origin")
				}
				if err := os.WriteFile(filepath.Join(target.directory, kind), nil, 0600); err != nil {
					t.Fatal(err)
				}
				moved := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.FrameX == -40 })
				if moved.Width != before.Width || moved.Height != before.Height || moved.ButtonX+moved.FrameX <= 0 {
					t.Fatal("move must retain window dimensions and a visible button center")
				}
				t.Logf("window translation: frame_x_before=%.0f frame_x_after=%.0f local_button_x=%.0f", before.FrameX, moved.FrameX, moved.ButtonX)
			}
			request := ActRequest{Kind: "click", ObservationRef: obs.Ref, Point: point, Screenshot: true}
			if mode == "pointer" {
				if before.LeftDowns != 0 || before.LeftUps != 0 || before.RightDowns != 0 || before.RightUps != 0 {
					t.Fatal("pointer fixture already received input")
				}
				request.Kind = kind
				request.Point = nativeImagePoint(t, obs, before, before.PointerX, before.PointerY)
			}
			if mode == "gestures" {
				if before.ScrollValue != 0 || before.SliderValue != 0 || before.DragToX <= before.DragFromX {
					t.Fatal("gesture fixture did not establish initial state")
				}
				request.Kind = kind
				if kind == "scroll" {
					request.Point = nativeImagePoint(t, obs, before, before.ScrollX, before.ScrollY)
					request.Direction, request.Amount = "down", 3
				} else {
					request.Point = nil
					request.Drag = &DragGesture{From: nativeImagePoint(t, obs, before, before.DragFromX, before.DragFromY), To: nativeImagePoint(t, obs, before, before.DragToX, before.DragToY), DurationMS: 500}
				}
			}
			priorFocus := readNativeState(t, sentinel)
			if !priorFocus.Active || priorFocus.FocusLosses != 0 {
				t.Fatal("sentinel lost focus before pixel dispatch")
			}
			result, err := run.actAndObserve(ctx, request)
			var facts struct{ Code, Effect string }
			_ = json.Unmarshal(result.Driver, &facts)
			t.Logf("case=%s timing=%+v outcome=%s driver_error=%v code=%s effect=%s", kind, result.Timing, result.Outcome, result.DriverError, facts.Code, facts.Effect)
			afterReply := readNativeState(t, target)
			settled := awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
			if controlMode == ForegroundAllowed {
				priorFocus := readNativeState(t, sentinel)
				if !priorFocus.Active || priorFocus.FocusLosses != 0 {
					t.Fatal("background refusal disturbed focus before explicit foreground input")
				}
				fresh := result.Observation
				if err != nil || !result.DriverError || facts.Code != "background_unavailable" || fresh == nil || settled.SliderValue != 0 || settled.Commits != 0 || settled.Value != before.Value {
					t.Fatal("foreground continuation lacks a verified pre-input refusal and fresh observation", err)
				}
				request.ObservationRef, request.DeliveryMode = fresh.Ref, "foreground"
				request.Drag = &DragGesture{From: nativeImagePoint(t, *fresh, settled, settled.DragFromX, settled.DragFromY), To: nativeImagePoint(t, *fresh, settled, settled.DragToX, settled.DragToY), DurationMS: 500}
				result, err = run.actAndObserve(ctx, request)
				facts = struct{ Code, Effect string }{}
				_ = json.Unmarshal(result.Driver, &facts)
				t.Logf("foreground drag timing=%+v outcome=%s driver_error=%v code=%s effect=%s", result.Timing, result.Outcome, result.DriverError, facts.Code, facts.Effect)
				afterReply = readNativeState(t, target)
				settled = awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
			}
			if mode == "gestures" {
				t.Logf("gesture readback: scroll_before=%.1f scroll_after=%.1f slider_before=%.1f slider_after=%.1f", before.ScrollValue, settled.ScrollValue, before.SliderValue, settled.SliderValue)
				matched := settled.ScrollValue > before.ScrollValue && settled.SliderValue == before.SliderValue
				if kind == "drag" {
					matched = settled.SliderValue >= 80 && settled.ScrollValue == before.ScrollValue
				}
				if !matched || settled.Commits != 0 || settled.Value != before.Value {
					t.Error("independent application state did not satisfy gesture postcondition")
				}
			}
			if kind == "resize" {
				if err != nil || !result.Dispatched || result.Outcome != "returned" || !result.DriverError || facts.Code != "capture_frame_mismatch" || facts.Effect != "refused" || settled.Commits != 0 || settled.Value != before.Value {
					t.Fatal("old capture was not refused before input", err)
				}
				fresh := result.Observation
				if fresh == nil || fresh.Image == nil || fresh.Degraded || run.observations[fresh.Ref].capture == "" {
					t.Fatal("geometry refusal did not return a usable fresh observation")
				}
				t.Logf("updated frame_x=%.0f frame_points=%.0fx%.0f image_pixels=%dx%d", settled.FrameX, settled.Width, settled.Height, fresh.ImageWidth, fresh.ImageHeight)
				result, err = run.actAndObserve(ctx, ActRequest{Kind: "click", ObservationRef: fresh.Ref, Point: nativeButtonPoint(t, *fresh, settled), Screenshot: true})
				t.Logf("case=%s recovery timing=%+v", kind, result.Timing)
			}
			nativeReturned(t, result, err)
			if result.Observation.Image == nil || run.observations[result.Observation.Ref].capture == "" {
				t.Fatal("pixel action did not return a verified fresh capture")
			}
			afterReply = readNativeState(t, target)
			settled = awaitNativeState(t, ctx, target, func(s nativeFixtureState) bool { return s.Ticks > afterReply.Ticks+3 })
			if mode == "pointer" {
				t.Logf("pointer native click calls=%d, pre-dispatch sentinel active=%v focus_losses=%d", calls["click"], priorFocus.Active, priorFocus.FocusLosses)
				if calls["click"] != 1 {
					t.Error("pointer action did not dispatch exactly one native click call")
				}
				t.Logf("pointer readback: left_downs=%d left_ups=%d right_downs=%d right_ups=%d max_count=%d invalid=%d distance_points=%.2f", settled.LeftDowns, settled.LeftUps, settled.RightDowns, settled.RightUps, settled.MaxClickCount, settled.InvalidPointerEvents, settled.PointerDistance)
				matched := settled.LeftDowns == 2 && settled.LeftUps == 2 && settled.RightDowns == 0 && settled.RightUps == 0 && settled.MaxClickCount == 2
				if kind == "right_click" {
					matched = settled.LeftDowns == 0 && settled.LeftUps == 0 && settled.RightDowns == 1 && settled.RightUps == 1 && settled.MaxClickCount == 1
				}
				if !matched || settled.InvalidPointerEvents != 0 || settled.PointerDistance > 2 || settled.Commits != 0 || settled.Result != before.Result || settled.Value != before.Value {
					t.Error("independent pointer event postcondition failed")
				}
			}
			if mode == "target" && (settled.Commits != 1 || settled.Result != "Result: AICE-314" || settled.Value != before.Value) {
				t.Fatalf("pixel click postcondition failed: commits=%d result_matches=%v input_unchanged=%v", settled.Commits, settled.Result == "Result: AICE-314", settled.Value == before.Value)
			}
			if kind == "move-left" && (settled.FrameX != -40 || calls["click"] != 1) {
				t.Error("moved-window click must retain the negative origin and dispatch exactly once")
			}
		})
	}
	if report, err := Inspect(ctx, driver, endpoint); err != nil || !report.ConnectionVerified {
		t.Fatal("pixel test cleanup stopped shared service", err)
	}
}

func nativeButtonPoint(t *testing.T, observation Observation, state nativeFixtureState) *Point {
	t.Helper()
	return nativeImagePoint(t, observation, state, state.ButtonX, state.ButtonY)
}

func nativeImagePoint(t *testing.T, observation Observation, state nativeFixtureState, x, y float64) *Point {
	t.Helper()
	if state.Width <= 0 || state.Height <= 0 || x <= 0 || y <= 0 || x >= state.Width || y >= state.Height {
		t.Fatal("fixture geometry unavailable")
	}
	// Convert independently reported AppKit points to the image actually sent
	// to the model. AICE/Driver remain responsible for their own inverse scales.
	return &Point{X: x * float64(observation.ImageWidth) / state.Width, Y: y * float64(observation.ImageHeight) / state.Height}
}
