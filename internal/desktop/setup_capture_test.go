package desktop

import (
	"context"
	"errors"
	"testing"
)

func TestSetupCaptureRequiresChoiceAndRetainsPartialFacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"ready", "cancel", "cancel-after-choice", "foreign", "empty", "missing-image", "invalid-image", "truncated-image", "multiple-images", "wrong-pid", "wrong-window", "malformed", "invalid-mapping", "transport-error", "retired-session", "changed-choice"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			selected := false
			image := pixelFixture(t)
			f := &fakeDriver{handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if kind == "empty" && name == "list_windows" {
					return structuredReply(map[string]any{"windows": []any{}}), nil, true
				}
				if name != "get_window_state" {
					return Reply{}, nil, false
				}
				if !selected || args["pid"] != 41 || args["window_id"] != uint64(99) || args["include_screenshot"] != true || args["include_accessibility_tree"] != false || args["session"] == "" {
					t.Fatal("capture did not preserve explicit choice and screenshot-only scope", args)
				}
				for _, field := range []string{"query", "max_elements", "max_depth"} {
					if _, exists := args[field]; exists {
						t.Fatal("setup requested semantic observation", field)
					}
				}
				wire := map[string]any{"pid": args["pid"], "window_id": args["window_id"], "capture_id": "capture", "screenshot_width": 2100, "screenshot_height": 2}
				switch kind {
				case "wrong-pid":
					wire["pid"] = 42
				case "wrong-window":
					wire["window_id"] = 100
				case "invalid-mapping":
					delete(wire, "capture_id")
				case "transport-error":
					return Reply{}, errors.New("synthetic connection loss"), true
				}
				reply := structuredReply(wire)
				reply.Images = []Image{{Data: image, MIMEType: "image/png"}}
				switch kind {
				case "missing-image":
					reply.Images = nil
				case "multiple-images":
					reply.Images = append(reply.Images, reply.Images[0])
				case "invalid-image":
					reply.Images[0].Data = []byte("invalid screenshot")
				case "truncated-image":
					reply.Images[0].Data = image[:33] // valid PNG header, missing pixels
				case "malformed":
					reply.Structured = []byte(`{`)
				}
				return reply, nil, true
			}}
			dials := 0
			m := newManager(func(context.Context) (driverClient, error) { dials++; return f, nil })
			m.platform = "linux"
			selections := 0
			result, err := setupWindowCapture(ctx, m, func(_ context.Context, windows []Window) (string, error) {
				selections++
				if f.count("get_window_state") != 0 {
					t.Fatal("captured before selecting a window")
				}
				switch kind {
				case "cancel":
					return "", context.Canceled
				case "cancel-after-choice":
					cancel()
				case "foreign":
					return "foreign-window", nil
				case "retired-session":
					if err := m.Disconnect(ctx); err != nil {
						t.Fatal(err)
					}
				case "changed-choice":
					windows[0].PID, windows[0].WindowID = 42, 100
				}
				selected = true
				return windows[0].Ref, nil
			})
			ready := kind == "ready" || kind == "changed-choice"
			if !result.ConnectionVerified || result.Ready != ready || result.CaptureVerified != ready || (err == nil) != ready || result.AuthorizationRequested || result.LaunchRequested {
				t.Fatal(result, err)
			}
			if (kind == "cancel" || kind == "cancel-after-choice") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			wantCaptures := 1
			switch kind {
			case "empty", "cancel", "cancel-after-choice", "foreign", "retired-session":
				wantCaptures = 0
			}
			if (kind == "empty" && selections != 0) || f.count("get_window_state") != wantCaptures || f.count("list_windows") != 1 || dials != 1 {
				t.Fatal("setup captured without a choice or retried")
			}
			wantEnds := 1
			if kind == "transport-error" || kind == "retired-session" {
				wantEnds = 0 // the connection was already retired
			}
			if f.closed != 1 || f.count("end_session") != wantEnds || m.Status().Connected || len(m.occupants) != 0 {
				t.Fatal("setup leaked its connection, session or occupancy")
			}
			if wantCaptures == 1 && (m.Status().CaptureAvailable != ready || m.Status().CaptureCheckedAt.IsZero()) {
				t.Fatal("capture status did not reflect validated screenshot")
			}
		})
	}
}

func TestSetupCaptureWithoutSelectorDoesNotConnect(t *testing.T) {
	m := newManager(func(context.Context) (driverClient, error) {
		t.Fatal("setup without a selector connected")
		return nil, nil
	})
	result, err := setupWindowCapture(t.Context(), m, nil)
	if err == nil || result != (SetupResult{}) {
		t.Fatal(result, err)
	}
}
