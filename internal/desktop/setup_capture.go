package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/media"
)

const maxTargets = 64

type windowIdentity struct {
	PID      int    `json:"pid"`
	WindowID uint64 `json:"window_id"`
}

// Window is metadata offered to the setup UI for an explicit capture choice.
// Ref is local to that selection; it is not a native execution reference.
type Window struct {
	Ref      string `json:"target_ref"`
	PID      int    `json:"pid"`
	WindowID uint64 `json:"window_id"`
	App      string `json:"app"`
	Title    string `json:"title"`
}

func setupWindows(ctx context.Context, run *Run) ([]Window, error) {
	ctx, release, err := run.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := run.ensureLocked(ctx); err != nil {
		return nil, err
	}
	reply, err := run.callLocked(ctx, "list_windows", map[string]any{})
	if err != nil {
		return nil, err
	}
	var wire struct {
		Windows []struct {
			windowIdentity
			App   string `json:"app_name"`
			Title string `json:"title"`
		} `json:"windows"`
	}
	if reply.IsError || json.Unmarshal(reply.Structured, &wire) != nil || wire.Windows == nil {
		return nil, errors.New("desktop: invalid setup window discovery response")
	}
	windows := make([]Window, 0, min(len(wire.Windows), maxTargets))
	for _, window := range wire.Windows {
		if window.PID <= 0 || window.WindowID == 0 {
			continue
		}
		windows = append(windows, Window{Ref: "window-" + rand.Text(), PID: window.PID, WindowID: window.WindowID, App: boundedText(window.App, 256), Title: boundedText(window.Title, 1024)})
		if len(windows) == maxTargets {
			break
		}
	}
	return windows, nil
}

func selectSetupWindow(ctx context.Context, windows []Window, selectWindow func(context.Context, []Window) (string, error)) (windowIdentity, error) {
	// The UI receives values, not authority to replace the discovered identity.
	ref, err := selectWindow(ctx, slices.Clone(windows))
	if err != nil {
		return windowIdentity{}, err
	}
	for _, window := range windows {
		if window.Ref == ref {
			return windowIdentity{PID: window.PID, WindowID: window.WindowID}, nil
		}
	}
	return windowIdentity{}, errors.New("desktop: setup requires a discovered window selection")
}

// verifySetupCapture uses the temporary setup session once, retaining neither
// the image nor tokens/snapshots/coordinates. It never reconnects after selection.
func verifySetupCapture(ctx context.Context, run *Run, target windowIdentity) (returnErr error) {
	ctx, release, err := run.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if !run.active || run.manager.client == nil {
		return errors.New("desktop: setup session unavailable; restart setup")
	}
	defer func() { run.manager.recordCapture(returnErr == nil) }()
	reply, err := run.callLocked(ctx, "get_window_state", map[string]any{
		"session": run.id, "pid": target.PID, "window_id": target.WindowID,
		"include_screenshot": true, "include_accessibility_tree": false,
		"max_image_dimension": 1600, "timeout_ms": 5000,
	})
	if err != nil {
		return err
	}
	var wire struct {
		windowIdentity
		Capture      string          `json:"capture_id"`
		Width        int             `json:"screenshot_width"`
		Height       int             `json:"screenshot_height"`
		FrameValid   *bool           `json:"screenshot_frame_valid"`
		CaptureError json.RawMessage `json:"screenshot_error"`
	}
	unavailable := serviceError("capture_unavailable", "the selected window did not produce a verified capture; inspect the graphical session and retry setup")
	if reply.IsError || json.Unmarshal(reply.Structured, &wire) != nil || wire.windowIdentity != target || wire.Capture == "" || len(wire.CaptureError) != 0 || len(reply.Images) != 1 {
		return unavailable
	}
	// The pinned X11 route omits frame validity on success. macOS requires
	// explicit positive frame validation; neither accepts an explicit failure.
	if (wire.FrameValid != nil && !*wire.FrameValid) || (run.manager.platform != "linux" && wire.FrameValid == nil) {
		return unavailable
	}
	part := reply.Images[0]
	config, _, err := media.Inspect(part.Data, part.MIMEType)
	if err != nil || wire.Width != config.Width || wire.Height != config.Height {
		return unavailable
	}
	// Validate the pixel payload too, without preparing a resized model image.
	if err := media.Validate(llm.ImageContent{Data: part.Data, MIMEType: part.MIMEType}); err != nil {
		return unavailable
	}
	return ctx.Err()
}

func boundedText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") + "…"
}
