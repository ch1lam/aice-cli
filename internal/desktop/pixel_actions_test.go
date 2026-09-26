package desktop

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"math"
	"testing"
)

func pixelFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2100, 2))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func pixelRequest(kind, ref string) ActRequest {
	request := ActRequest{Kind: kind, ObservationRef: ref}
	switch kind {
	case "drag":
		request.Drag = &DragGesture{From: &Point{X: 1000, Y: 0.5}, To: &Point{X: 1500, Y: 0.5}}
	case "scroll":
		request.Point, request.Direction = &Point{X: 1000, Y: 0.5}, "down"
	case "type_text":
		request.Point, request.Text = &Point{X: 1000, Y: 0.5}, "synthetic"
	}
	return request
}

func TestPixelActionsUseSessionScreenshotCoordinates(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"scroll", "type_text", "drag"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeDriver{image: pixelFixture(t), handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if name != kind {
					return Reply{}, nil, false
				}
				if args["pid"] != 41 || args["window_id"] != uint64(99) || args["delivery_mode"] != "background" || args["session"] == "" || args["capture_id"] != nil || args["scope"] != nil || args["snapshot_id"] != nil {
					t.Fatal("pixel route escaped its public session/window schema", args)
				}
				if kind == "drag" {
					if args["from_x"] != float64(1050) || args["from_y"] != float64(1) || args["to_x"] != float64(1575) || args["to_y"] != float64(1) || args["duration_ms"] != 500 || args["x"] != nil {
						t.Fatal("drag mapping differs from actual image", args)
					}
					return Reply{IsError: true, Structured: []byte(`{"code":"background_unavailable"}`)}, nil, true
				}
				if args["x"] != float64(1050) || args["y"] != float64(1) {
					t.Fatal("pixel mapping differs from actual image", args)
				}
				return structuredReply(map[string]any{"effect": "unverifiable"}), nil, true
			}}
			_, r := testRun(t, f, true)
			o := observed(t, r, true)
			request := pixelRequest(kind, o.Ref)
			result, err := r.Act(t.Context(), request)
			if err != nil || result.Observation == nil || result.Observation.ForegroundAction != "" || f.count(kind) != 1 || f.count("get_window_state") != 2 {
				t.Fatal("pixel action failed or background-only mode offered foreground", result, err)
			}
			if _, err := r.Act(t.Context(), request); err == nil || f.count(kind) != 1 {
				t.Fatal("pixel action replayed", err)
			}
			for _, call := range f.calls {
				if (call.name == kind || call.name == "get_window_state") && call.args["session"] != r.id {
					t.Fatal("screenshot and input used different sessions")
				}
			}
		})
	}
}

func TestPixelForegroundNeedsFreshImageAndExplicitChoice(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"scroll", "drag", "type_text"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeDriver{image: pixelFixture(t)}
			f.handle = func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if name != kind {
					return Reply{}, nil, false
				}
				if args["delivery_mode"] == "background" {
					if kind == "type_text" {
						return Reply{IsError: true, Structured: []byte(`{"code":"SCREEN_SHARING_REQUIRES_FOREGROUND_HID","effect":"refused"}`)}, nil, true
					}
					return Reply{IsError: true, Structured: []byte(`{"code":"background_unavailable"}`)}, nil, true
				}
				if args["delivery_mode"] != "foreground" {
					t.Fatal("implicit foreground mode", args)
				}
				return structuredReply(map[string]any{"effect": "unverifiable"}), nil, true
			}
			_, r := testRun(t, f, true)
			r.options.Mode = ForegroundAllowed
			o := observed(t, r, true)
			request := pixelRequest(kind, o.Ref)
			result, err := r.Act(t.Context(), request)
			if err != nil || result.Observation == nil || result.Observation.Image == nil || result.Observation.ForegroundAction != kind || f.count(kind) != 1 {
				t.Fatal("pixel opportunity had no grounding image or was replayed", result, err)
			}
			request.ObservationRef, request.DeliveryMode = result.Observation.Ref, "foreground"
			if kind == "drag" {
				// Re-ground in the new image; these are never copied internally.
				request.Drag.From.X, request.Drag.To.X = 1200, 1400
			} else {
				request.Point.X = 1200
			}
			result, err = r.Act(t.Context(), request)
			if err != nil || result.Observation == nil || result.Observation.ForegroundAction != "" || f.count(kind) != 2 {
				t.Fatal("explicit foreground dispatch failed", result, err)
			}
		})
	}
}

func TestPixelRefusalCannotOfferForegroundWithoutVerifiedFollowupImage(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"missing_image", "bad_image", "transport", "partial", "unverifiable", "foreign_identity", "null_effect", "extra_outcome"} {
		t.Run(failure, func(t *testing.T) {
			f := &fakeDriver{image: pixelFixture(t)}
			_, r := testRun(t, f, true)
			r.options.Mode = ForegroundAllowed
			o := observed(t, r, true)
			f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
				if name != "drag" {
					return Reply{}, nil, false
				}
				reply := Reply{IsError: true, Structured: []byte(`{"code":"background_unavailable"}`)}
				switch failure {
				case "missing_image":
					f.image = nil
				case "bad_image":
					f.image = []byte("invalid image")
				case "transport":
					return reply, io.EOF, true
				case "partial", "unverifiable":
					reply = structuredReply(map[string]any{"code": "background_unavailable", "effect": failure})
					reply.IsError = true
				case "foreign_identity":
					reply.Structured = []byte(`{"code":"background_unavailable","pid":42,"window_id":99}`)
				case "null_effect":
					reply.Structured = []byte(`{"code":"background_unavailable","effect":null}`)
				case "extra_outcome":
					reply.Structured = []byte(`{"code":"background_unavailable","outcome":"unknown"}`)
				}
				return reply, nil, true
			}
			result, err := r.Act(t.Context(), pixelRequest("drag", o.Ref))
			if err != nil || (result.Observation != nil && result.Observation.ForegroundAction != "") || f.count("drag") != 1 {
				t.Fatal("invalid refusal offered foreground", result, err)
			}
		})
	}
}

func TestPixelActionsRejectUngroundedOrInvalidGestures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*ActRequest, *observationBinding)
	}{
		{"no_capture", func(_ *ActRequest, b *observationBinding) { b.capture = "" }},
		{"no_snapshot", func(_ *ActRequest, b *observationBinding) { b.snapshot = "" }},
		{"no_gesture", func(r *ActRequest, _ *observationBinding) { r.Drag = nil }},
		{"missing_end", func(r *ActRequest, _ *observationBinding) { r.Drag.To = nil }},
		{"outside_end", func(r *ActRequest, _ *observationBinding) { r.Drag.To.X = 2000 }},
		{"negative_start", func(r *ActRequest, _ *observationBinding) { r.Drag.From.X = -1 }},
		{"nan", func(r *ActRequest, _ *observationBinding) { r.Drag.To.Y = math.NaN() }},
		{"infinite", func(r *ActRequest, _ *observationBinding) { r.Drag.From.X = math.Inf(1) }},
		{"long_duration", func(r *ActRequest, _ *observationBinding) { r.Drag.DurationMS = 10001 }},
		{"negative_duration", func(r *ActRequest, _ *observationBinding) { r.Drag.DurationMS = -1 }},
		{"mixed_point", func(r *ActRequest, _ *observationBinding) { r.Point = &Point{} }},
		{"mixed_token", func(r *ActRequest, _ *observationBinding) { r.ElementToken = "token" }},
		{"mixed_text", func(r *ActRequest, _ *observationBinding) { r.Text = "ignored" }},
		{"wrong_kind", func(r *ActRequest, _ *observationBinding) { r.Kind = "click" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{image: pixelFixture(t)}
			_, r := testRun(t, f, true)
			o := observed(t, r, true)
			request := pixelRequest("drag", o.Ref)
			binding := r.observations[o.Ref]
			tc.change(&request, &binding)
			r.observations[o.Ref] = binding
			before := len(f.calls)
			if _, err := r.Act(t.Context(), request); err == nil || len(f.calls) != before {
				t.Fatal("invalid gesture dispatched", err)
			}
		})
	}
	for _, kind := range []string{"scroll", "type_text", "drag"} {
		f := &fakeDriver{}
		_, r := testRun(t, f, false)
		o := observed(t, r, false)
		if _, err := r.Act(t.Context(), pixelRequest(kind, o.Ref)); err == nil || f.count(kind) != 0 {
			t.Fatal("semantic-only model dispatched pixels", kind, err)
		}
	}
}
