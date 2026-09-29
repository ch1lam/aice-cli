package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type fakeDriverCall struct {
	name string
	args map[string]any
}
type fakeDriver struct {
	mu       sync.Mutex
	calls    []fakeDriverCall
	snapshot int
	closed   int
	handle   func(context.Context, string, map[string]any) (Reply, error, bool)
	image    []byte
}

func structuredReply(value any) Reply { data, _ := json.Marshal(value); return Reply{Structured: data} }

func (f *fakeDriver) call(ctx context.Context, name string, arguments any) (Reply, error) {
	args := arguments.(map[string]any)
	f.mu.Lock()
	f.calls = append(f.calls, fakeDriverCall{name, args})
	f.mu.Unlock()
	if f.handle != nil {
		if reply, err, handled := f.handle(ctx, name, args); handled {
			return reply, err
		}
	}
	switch name {
	case "start_session":
		return structuredReply(map[string]any{"active": true}), nil
	case "end_session":
		return structuredReply(map[string]any{"ended": true}), nil
	case "list_windows":
		return structuredReply(map[string]any{"windows": []any{
			map[string]any{"pid": 41, "window_id": 99, "app_name": "Synthetic editor", "title": "Synthetic document"},
			map[string]any{"pid": 42, "window_id": 100, "app_name": "Synthetic form", "title": "Synthetic form"},
		}}), nil
	case "get_window_state":
		f.snapshot++
		r := structuredReply(map[string]any{"pid": args["pid"], "window_id": args["window_id"], "snapshot_id": fmt.Sprint("snapshot-", f.snapshot), "capture_id": fmt.Sprint("capture-", f.snapshot), "screenshot_width": 2100, "screenshot_height": 2, "screenshot_frame_valid": true, "elements_complete": false, "elements": []any{map[string]any{"element_token": fmt.Sprint("opaque-token-", f.snapshot), "role": "AXTextField", "value": "synthetic"}}})
		if args["include_screenshot"] == true && f.image != nil {
			r.Images = []Image{{Data: f.image, MIMEType: "image/png"}}
		}
		return r, nil
	case "click", "type_text", "set_value":
		return structuredReply(map[string]any{"effect": "unverifiable"}), nil
	default:
		return Reply{}, errors.New("unexpected tool " + name)
	}
}
func (f *fakeDriver) close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closed++; return nil }
func (f *fakeDriver) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.name == name {
			n++
		}
	}
	return n
}

func testRun(t *testing.T, f *fakeDriver, images bool) (*Manager, *Run) {
	t.Helper()
	m := newManager(func(context.Context) (driverClient, error) { return f, nil })
	r, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = m.Close() })
	return m, r
}
func observed(t *testing.T, r *Run, screenshot bool) Observation {
	t.Helper()
	d, err := r.discoverWindows(t.Context(), "editor", 8)
	if err != nil || len(d.Windows) != 1 {
		t.Fatalf("discovery=%+v err=%v", d, err)
	}
	o, err := r.observeWindow(t.Context(), ObserveRequest{TargetRef: d.Windows[0].Ref, Screenshot: screenshot})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func setupObserved(t *testing.T, r *Run, screenshot bool) Observation {
	t.Helper()
	windows, err := r.Windows(t.Context(), "editor", 8)
	if err != nil || len(windows.Windows) != 1 {
		t.Fatal("setup windows", err)
	}
	observation, err := r.Observe(t.Context(), ObserveRequest{TargetRef: windows.Windows[0].Ref, Screenshot: screenshot})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
