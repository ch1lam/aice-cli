package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"sync"
	"testing"
	"time"
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
	d, err := r.Windows(t.Context(), "editor", 8)
	if err != nil || len(d.Windows) != 1 {
		t.Fatalf("discovery=%+v err=%v", d, err)
	}
	o, err := r.Observe(t.Context(), ObserveRequest{TargetRef: d.Windows[0].Ref, Screenshot: screenshot})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDesktopRunLifecycleAndActObserve(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	m, r := testRun(t, f, false)
	if m.Status().Connected || len(f.calls) != 0 {
		t.Fatal("binding or status performed native I/O")
	}
	o := observed(t, r, false)
	result, err := r.Act(t.Context(), ActRequest{Kind: "type_text", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token, Text: "synthetic update"})
	if err != nil || !result.Dispatched || result.Observation == nil || result.Outcome != "returned" || !bytes.Contains(result.Driver, []byte("unverifiable")) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err == nil {
		t.Fatal("consumed observation reused")
	}
	if f.count("start_session") != 1 || f.count("get_window_state") != 2 || f.count("type_text") != 1 {
		t.Fatal("unexpected repeated discovery/session/action")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if f.count("end_session") != 1 || f.closed != 0 {
		t.Fatal("run cleanup ended connection or repeated cleanup")
	}
	if _, err := r.Windows(t.Context(), "", 8); err == nil {
		t.Fatal("closed binding stayed usable")
	}
}

func TestDesktopUnknownActionNeverReplayed(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{handle: func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "click" {
			return Reply{}, io.EOF, true
		}
		return Reply{}, nil, false
	}}
	m, r := testRun(t, f, false)
	o := observed(t, r, false)
	result, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token})
	if err != nil || !result.Dispatched || result.Outcome != "unknown" || result.Observation != nil || m.Status().Connected {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err == nil {
		t.Fatal("lost-response action replayed")
	}
	if f.count("click") != 1 {
		t.Fatal("action repeated")
	}
	if _, err := r.Observe(t.Context(), ObserveRequest{TargetRef: o.TargetRef}); err == nil {
		t.Fatal("old target survived disconnected generation")
	}
	_ = observed(t, r, false)
	if m.Status().Generation != 2 || f.count("click") != 1 {
		t.Fatal("reconnect reused old generation")
	}
}

func TestDesktopPostObservationFailurePreservesAction(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, r := testRun(t, f, false)
	o := observed(t, r, false)
	f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "get_window_state" {
			return Reply{}, io.EOF, true
		}
		return Reply{}, nil, false
	}
	result, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token})
	if err != nil || result.Outcome != "returned" || len(result.Driver) == 0 || result.ObservationError == "" || result.Observation != nil || f.count("click") != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDesktopMissingCapabilityWasNotDispatched(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{handle: func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "type_text" {
			return Reply{}, beforeDispatchError{errors.New("capability unavailable")}, true
		}
		return Reply{}, nil, false
	}}
	m, r := testRun(t, f, false)
	o := observed(t, r, false)
	result, err := r.Act(t.Context(), ActRequest{Kind: "type_text", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token, Text: "synthetic"})
	if err != nil || result.Dispatched || result.Outcome != "not_dispatched" || !m.Status().Connected {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDesktopSnapshotAndCrossRunIsolation(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	m, first := testRun(t, f, false)
	o := observed(t, first, false)
	second, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err == nil {
		t.Fatal("observation crossed run identity")
	}
	_ = observed(t, second, false)
	if _, err := first.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err == nil {
		t.Fatal("same-window snapshot refresh did not invalidate first run")
	}
	if f.count("click") != 0 {
		t.Fatal("stale action dispatched")
	}
}

func TestDesktopImageMappingAndSemanticFallback(t *testing.T) {
	t.Parallel()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2100, 2))); err != nil {
		t.Fatal(err)
	}
	f := &fakeDriver{image: data.Bytes()}
	_, r := testRun(t, f, true)
	o := observed(t, r, true)
	if o.Image == nil || o.ImageWidth != 2000 || o.ImageHeight != 1 || o.Image.Original == nil {
		t.Fatalf("image mapping lost: %+v", o)
	}
	result, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, Point: &Point{X: 1000, Y: 0.5}})
	if err != nil || !result.Dispatched {
		t.Fatal(result, err)
	}
	for _, c := range f.calls {
		if c.name == "click" {
			if c.args["x"] != float64(1050) || c.args["y"] != float64(1) || c.args["capture_id"] != "capture-1" || c.args["delivery_mode"] != "background" {
				t.Fatal(c.args)
			}
		}
	}
	o = observed(t, r, false)
	if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, Point: &Point{X: 1, Y: 1}}); err == nil {
		t.Fatal("semantic observation accepted pixels")
	}
	f.image = []byte("not an image")
	o = observed(t, r, true)
	if !o.Degraded || o.Image != nil || len(o.Elements) != 1 {
		t.Fatal("bad screenshot discarded semantic evidence")
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Point{{X: math.NaN()}, {X: math.Inf(1)}, {X: -1}, {X: 2000}} {
		if _, _, err := (observationBinding{capture: "capture", width: 2000, height: 1}).pixel(p.X, p.Y); err == nil {
			t.Fatal("invalid pixel accepted")
		}
	}
}

func TestDesktopCancelStopsQueuedMutation(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	f := &fakeDriver{handle: func(ctx context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "click" {
			close(entered)
			<-ctx.Done()
			return Reply{}, ctx.Err(), true
		}
		return Reply{}, nil, false
	}}
	_, r := testRun(t, f, false)
	o := observed(t, r, false)
	done := make(chan ActResult, 1)
	go func() {
		result, _ := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token})
		done <- result
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("action did not start")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Outcome != "unknown" {
			t.Fatal(result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not settle")
	}
	if _, err := r.Act(t.Context(), ActRequest{Kind: "click", ObservationRef: o.Ref, ElementToken: o.Elements[0].Token}); err == nil {
		t.Fatal("cancelled run dispatched another action")
	}
	if f.count("click") != 1 {
		t.Fatal("cancel repeated mutation")
	}
}
