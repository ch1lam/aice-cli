package desktop

import (
	"context"
	"errors"
	"testing"
)

func TestSetupCaptureRequiresChoiceAndRetainsPartialFacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"ready", "cancel", "foreign", "empty", "missing-image", "invalid-mapping"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeDriver{image: pixelFixture(t), handle: func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				if kind == "empty" && name == "list_windows" {
					return structuredReply(map[string]any{"windows": []any{}}), nil, true
				}
				if name == "get_window_state" && (kind == "missing-image" || kind == "invalid-mapping") {
					reply := structuredReply(map[string]any{"pid": args["pid"], "window_id": args["window_id"]})
					if kind == "invalid-mapping" {
						reply.Images = []Image{{Data: pixelFixture(t), MIMEType: "image/png"}}
					}
					return reply, nil, true
				}
				return Reply{}, nil, false
			}}
			m := newManager(func(context.Context) (driverClient, error) { return f, nil })
			m.platform = "linux"
			selections := 0
			result, err := setupWindowCapture(t.Context(), m, func(_ context.Context, windows []Window) (string, error) {
				selections++
				if f.count("get_window_state") != 0 {
					t.Fatal("captured before selecting a window")
				}
				if kind == "cancel" {
					return "", context.Canceled
				}
				if kind == "foreign" {
					return "foreign-window", nil
				}
				return windows[0].Ref, nil
			})
			if !result.ConnectionVerified || result.Ready != (kind == "ready") || result.CaptureVerified != (kind == "ready") || (err == nil) != (kind == "ready") || result.AuthorizationRequested || result.LaunchRequested {
				t.Fatal(result, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if (kind == "empty" && selections != 0) || ((kind == "cancel" || kind == "foreign") && f.count("get_window_state") != 0) {
				t.Fatal("unselected capture dispatched")
			}
			if f.closed != 1 || f.count("end_session") != 1 || m.Status().Connected {
				t.Fatal("setup leaked its connection or native session")
			}
		})
	}
}
