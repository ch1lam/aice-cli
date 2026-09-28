package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

type managedFake struct {
	*fakeDriver
	catalog    mcpclient.Catalog[mcpclient.Tool]
	generation atomic.Uint64
}

func (f *managedFake) Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	return f.catalog, nil
}
func (f *managedFake) ToolGeneration() uint64 { return f.generation.Load() }
func (f *managedFake) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if check != nil {
		if err := check(ctx); err != nil {
			return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
		}
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	reply, err := f.call(ctx, name, args)
	result := mcpclient.Result{StructuredContent: reply.Structured, IsError: reply.IsError, State: llm.ExecutionReturned}
	if err != nil {
		result.State = llm.ExecutionUnknown
	}
	for _, text := range reply.Text {
		result.Content = append(result.Content, mcpclient.Block{Kind: mcpclient.BlockText, Text: text})
	}
	for _, image := range reply.Images {
		result.Content = append(result.Content, mcpclient.Block{Kind: mcpclient.BlockImage, Data: image.Data, MIMEType: image.MIMEType})
	}
	return result, err
}
func managedTestRun(t *testing.T, platform string, mode ControlMode, images bool) (*Manager, *Run, *managedFake) {
	t.Helper()
	f := &managedFake{fakeDriver: &fakeDriver{}}
	schemas := schemaFixture(t)
	if platform == "linux" {
		schemas = linuxFullSchemaFixture(t)
	}
	for name, schema := range schemas {
		f.catalog.Items = append(f.catalog.Items, mcpclient.Tool{Name: name, InputSchema: schema})
	}
	f.catalog.Generation, f.catalog.Complete = 1, true
	f.generation.Store(1)
	m := newManager(func(context.Context) (driverClient, error) { return f, nil })
	m.platform = platform
	r, err := m.Bind(t.Context(), RunOptions{Mode: mode, Images: images})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = m.Close() })
	return m, r, f
}
func managedInvoke(t *testing.T, r *Run, name, args string) mcpclient.Result {
	t.Helper()
	result, err := r.CallChecked(t.Context(), name, []byte(args), nil)
	if err != nil || result.State != llm.ExecutionReturned {
		t.Fatalf("%s state=%s err=%v", name, result.State, err)
	}
	return result
}
func managedObserve(t *testing.T, r *Run, screenshot bool) string {
	t.Helper()
	args := `{"pid":41,"window_id":99,"include_screenshot":false}`
	if screenshot {
		args = `{"pid":41,"window_id":99,"include_screenshot":true}`
	}
	result := managedInvoke(t, r, "get_window_state", args)
	var state struct {
		Elements []Element `json:"elements"`
	}
	if json.Unmarshal(result.StructuredContent, &state) != nil || len(state.Elements) == 0 {
		t.Fatal("observation lost")
	}
	return state.Elements[0].Token
}
func managedClick(token string) string {
	data, _ := json.Marshal(map[string]any{"pid": 41, "window_id": 99, "element_token": token})
	return string(data)
}

func TestManagedRunCatalogAndLifecycle(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			m, r, f := managedTestRun(t, platform, BackgroundOnly, false)
			if m.Status().Connected || f.count("start_session") != 0 {
				t.Fatal("binding caused I/O")
			}
			catalog, err := r.Tools(t.Context())
			if err != nil || !catalog.Complete || len(catalog.Items) != 11 || catalog.Generation != r.ToolGeneration() {
				t.Fatal("managed catalog", err)
			}
			for _, entry := range catalog.Items {
				if entry.Name == "start_session" || entry.Name == "end_session" || entry.Name == "check_permissions" || entry.Name == "get_config" {
					t.Fatal("lifecycle/setup exposed")
				}
				var schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				}
				if json.Unmarshal(entry.InputSchema, &schema) != nil {
					t.Fatal("invalid projected schema")
				}
				for _, field := range []string{"session", "scope", "target", "screenshot_out_file", "debug_image_out", "additional_arguments", "urls", "webkit_inspector_port"} {
					if _, ok := schema.Properties[field]; ok {
						t.Fatal("authority escape field exposed", entry.Name, field)
					}
				}
				if entry.Name == "list_windows" && len(schema.Required) != 0 {
					t.Fatal("discovery requires unknown pid")
				}
			}
			managedInvoke(t, r, "list_windows", `{}`)
			token := managedObserve(t, r, false)
			action := managedInvoke(t, r, "click", managedClick(token))
			if !bytes.Contains(action.StructuredContent, []byte("unverifiable")) || f.count("click") != 1 || f.count("get_window_state") != 1 {
				t.Fatal("action result lost or implicit extra RPC")
			}
			result, err := r.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
			if err == nil || result.State != llm.ExecutionNotDispatched || f.count("click") != 1 {
				t.Fatal("observation replayed")
			}
			fresh := managedObserve(t, r, false)
			managedInvoke(t, r, "click", managedClick(fresh))
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if f.count("start_session") != 1 || f.count("end_session") != 1 || r.ToolGeneration() == catalog.Generation {
				t.Fatal("session ownership or close invalidation lost")
			}
		})
	}
}

func TestManagedRunRejectsUnownedAndExtraAuthority(t *testing.T) {
	t.Parallel()
	_, r, f := managedTestRun(t, "darwin", BackgroundOnly, false)
	before, err := r.CallChecked(t.Context(), "list_windows", []byte(`{}`), nil)
	if err == nil || before.State != llm.ExecutionNotDispatched || f.count("start_session") != 0 {
		t.Fatal("execution connected implicitly")
	}
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, args string }{
		{"start_session", `{}`}, {"get_config", `{}`}, {"check_permissions", `{"prompt":true}`},
		{"get_window_state", `{"pid":41,"window_id":99,"include_screenshot":false}`},
		{"list_windows", `{"pid":41,"pid":42}`}, {"list_windows", `{"pid":null}`},
		{"list_windows", `{"pid":0}`}, {"list_windows", `{"pid":1.5}`}, {"list_windows", `{} {}`},
		{"launch_app", `{"bundle_id":"undiscovered"}`},
		{"launch_app", `{"bundle_id":"known","urls":["https://example.com"]}`},
		{"get_window_state", `{"pid":41,"window_id":99,"screenshot_out_file":"/tmp/no"}`},
		{"click", `{"pid":41,"window_id":99,"element_token":"old","session":"foreign"}`},
		{"click", `{"pid":41,"window_id":99,"x":1}`},
	} {
		result, err := r.CallChecked(t.Context(), tc.name, []byte(tc.args), nil)
		if err == nil || result.State != llm.ExecutionNotDispatched {
			t.Fatal("unsafe arguments accepted", tc.name, tc.args, err)
		}
	}
	if f.count("list_windows") != 0 || f.count("get_window_state") != 0 || f.count("click") != 0 || f.count("launch_app") != 0 {
		t.Fatal("rejected arguments reached native service")
	}
}

func TestManagedRunFinalPermitAndUnknownOutcome(t *testing.T) {
	t.Parallel()
	m, r, f := managedTestRun(t, "darwin", BackgroundOnly, false)
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	token := managedObserve(t, r, false)
	denied := errors.New("permit revoked")
	checks := 0
	result, err := r.CallChecked(t.Context(), "click", []byte(managedClick(token)), func(context.Context) error {
		checks++
		if checks == 2 {
			return denied
		}
		return nil
	})
	if !errors.Is(err, denied) || result.State != llm.ExecutionNotDispatched || checks != 2 || f.count("click") != 0 {
		t.Fatal("final permit did not stop dispatch", err)
	}
	token = managedObserve(t, r, false)
	f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "click" {
			return Reply{}, io.EOF, true
		}
		return Reply{}, nil, false
	}
	result, err = r.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
	if err == nil || result.State != llm.ExecutionUnknown || m.Status().Connected || f.count("click") != 1 {
		t.Fatal("unknown mutation lost or replayed", err)
	}
	result, err = r.CallChecked(t.Context(), "list_windows", []byte(`{}`), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched || f.count("start_session") != 1 {
		t.Fatal("execution silently reconnected")
	}
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.count("start_session") != 2 || f.count("click") != 1 {
		t.Fatal("explicit discovery did not re-admit cleanly")
	}
}

func TestManagedRunImageCoordinatesUseGenericMediaDimensions(t *testing.T) {
	t.Parallel()
	_, r, f := managedTestRun(t, "darwin", BackgroundOnly, true)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2100, 2))); err != nil {
		t.Fatal(err)
	}
	f.image = data.Bytes()
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	managedObserve(t, r, true)
	result := managedInvoke(t, r, "click", `{"pid":41,"window_id":99,"x":1000,"y":0.5}`)
	if result.State != llm.ExecutionReturned {
		t.Fatal("pixel action rejected")
	}
	for _, call := range f.calls {
		if call.name == "click" && (call.args["x"] != float64(1050) || call.args["y"] != float64(1) || call.args["capture_id"] != "capture-1") {
			t.Fatal("generic image coordinates not mapped", call.args)
		}
	}
}

func TestManagedRunForegroundRequiresFreshVerifiedRefusal(t *testing.T) {
	t.Parallel()
	_, r, f := managedTestRun(t, "darwin", ForegroundAllowed, false)
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	token := managedObserve(t, r, false)
	args := func(token, delivery, text string) []byte {
		data, _ := json.Marshal(map[string]any{"pid": 41, "window_id": 99, "element_token": token, "text": text, "delivery_mode": delivery})
		return data
	}
	result, err := r.CallChecked(t.Context(), "type_text", args(token, "foreground", "task"), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched {
		t.Fatal("foreground without refusal")
	}
	f.handle = func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
		if name == "type_text" && args["delivery_mode"] == "background" {
			return backgroundRefusal(), nil, true
		}
		return Reply{}, nil, false
	}
	result, err = r.CallChecked(t.Context(), "type_text", args(token, "background", "task"), nil)
	if err != nil || !result.IsError || f.count("type_text") != 1 {
		t.Fatal("refusal lost", err)
	}
	token = managedObserve(t, r, false)
	result, err = r.CallChecked(t.Context(), "type_text", args(token, "foreground", "different"), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched {
		t.Fatal("foreground changed intended action")
	}
	result, err = r.CallChecked(t.Context(), "type_text", args(token, "foreground", "task"), nil)
	if err != nil || result.State != llm.ExecutionReturned || f.count("type_text") != 2 {
		t.Fatal("explicit continuation failed", err)
	}
	for _, c := range f.calls {
		if c.name == "type_text" && (c.args["session"] != r.id || c.args["target"] != nil || c.args["scope"] != nil) {
			t.Fatal("native identity not owned")
		}
	}
}

func TestManagedRunDoesNotAdmitForbiddenSchemaFields(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"number"}},"required":["x"],"allOf":[]}`)
	if _, err := managedSchema("click", raw, []string{"x"}); err == nil {
		t.Fatal("unreviewed schema shape projected")
	}
	for _, input := range []string{`{"pid":41,"window_id":99,"scope":"desktop"}`, `{"pid":41,"window_id":99,"capture_id":"saved"}`, `{"pid":41,"window_id":99,"element_index":1}`} {
		if _, err := decodeManagedArguments("click", "darwin", []byte(input)); err == nil {
			t.Fatal("alternate target accepted", input)
		}
	}
	if _, err := decodeManagedArguments("click", "darwin", []byte(`{"pid":41,"window_id":99,"x":"`+strings.Repeat("x", 1<<20)+`"}`)); err == nil {
		t.Fatal("oversized input accepted")
	}
}

func TestManagedRunLaunchUsesOnlyDiscoveredIdentity(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			_, r, f := managedTestRun(t, platform, BackgroundOnly, false)
			f.handle = func(_ context.Context, name string, args map[string]any) (Reply, error, bool) {
				switch name {
				case "list_apps":
					return structuredReply(map[string]any{"apps": []any{map[string]any{"bundle_id": "app.fixture", "launch_path": "/usr/share/applications/fixture.desktop"}}}), nil, true
				case "launch_app":
					if len(args) != 1 || (platform == "linux" && args["launch_path"] != "/usr/share/applications/fixture.desktop") || (platform == "darwin" && args["bundle_id"] != "app.fixture") {
						t.Fatal("launch changed discovered identity", args)
					}
					return structuredReply(map[string]any{"pid": 41, "name": "Fixture"}), nil, true
				}
				return Reply{}, nil, false
			}
			if _, err := r.Tools(t.Context()); err != nil {
				t.Fatal(err)
			}
			managedInvoke(t, r, "list_apps", `{}`)
			args := `{"bundle_id":"app.fixture"}`
			if platform == "linux" {
				args = `{"launch_path":"/usr/share/applications/fixture.desktop"}`
			}
			managedInvoke(t, r, "launch_app", args)
			result, err := r.CallChecked(t.Context(), "launch_app", []byte(args), nil)
			if err == nil || result.State != llm.ExecutionNotDispatched || f.count("launch_app") != 1 {
				t.Fatal("launch identity replayed")
			}
		})
	}
}

func TestManagedRunCrossRunAndCatalogInvalidation(t *testing.T) {
	t.Parallel()
	m, first, f := managedTestRun(t, "darwin", BackgroundOnly, false)
	original, err := first.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, first, "list_windows", `{}`)
	token := managedObserve(t, first, false)
	second, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := second.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched {
		t.Fatal("window/token crossed run")
	}
	managedInvoke(t, second, "list_windows", `{}`)
	managedObserve(t, second, false)
	result, err = first.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched {
		t.Fatal("another run's observation did not invalidate token")
	}
	token = managedObserve(t, first, false)
	f.generation.Add(1)
	if first.ToolGeneration() == original.Generation {
		t.Fatal("catalog notification retained executable epoch")
	}
	result, err = first.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched || f.count("click") != 0 {
		t.Fatal("stale catalog executed")
	}
}

func TestManagedRunCloseCancelsOneMutationWithoutReplay(t *testing.T) {
	t.Parallel()
	m, r, f := managedTestRun(t, "darwin", BackgroundOnly, false)
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	token := managedObserve(t, r, false)
	entered := make(chan struct{})
	f.handle = func(ctx context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "click" {
			close(entered)
			<-ctx.Done()
			return Reply{}, ctx.Err(), true
		}
		return Reply{}, nil, false
	}
	done := make(chan mcpclient.Result, 1)
	go func() {
		result, _ := r.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
		done <- result
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not start")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.State != llm.ExecutionUnknown {
			t.Fatal("canceled mutation not unknown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not settle")
	}
	result, err := r.CallChecked(t.Context(), "click", []byte(managedClick(token)), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched || m.Status().Connected || f.count("click") != 1 {
		t.Fatal("canceled run retained execution")
	}
}

func TestManagedRunThroughAdmittedMCPWire(t *testing.T) {
	t.Parallel()
	config, peer := fakeTransport(t, "managed-run")
	m := newManager(func(ctx context.Context) (driverClient, error) { return connect(ctx, config) })
	defer m.Close()
	r, err := m.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	catalog, err := r.Tools(t.Context())
	if err != nil || len(catalog.Items) != 11 {
		t.Fatal("wire catalog did not admit managed operations", err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	token := managedObserve(t, r, false)
	managedInvoke(t, r, "click", managedClick(token))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if peer.initializes.Load() != 1 || peer.pages.Load() != 2 || peer.calls.Load() != 5 {
		t.Fatal("wire lifecycle replayed or used another connection", peer.calls.Load())
	}
}

func TestManagedRunCannotUsePixelsOmittedByResultBlockLimit(t *testing.T) {
	t.Parallel()
	_, r, f := managedTestRun(t, "darwin", BackgroundOnly, true)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name != "get_window_state" {
			return Reply{}, nil, false
		}
		reply := structuredReply(map[string]any{"pid": 41, "window_id": 99, "snapshot_id": "snapshot", "capture_id": "capture", "screenshot_width": 2, "screenshot_height": 2, "screenshot_frame_valid": true, "elements": []any{map[string]any{"element_token": "token"}}})
		reply.Text = make([]string, 256)
		reply.Images = []Image{{Data: data.Bytes(), MIMEType: "image/png"}}
		return reply, nil, true
	}
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	managedObserve(t, r, true)
	result, err := r.CallChecked(t.Context(), "click", []byte(`{"pid":41,"window_id":99,"x":1,"y":1}`), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched || f.count("click") != 0 {
		t.Fatal("undeliverable screenshot granted pixels")
	}
	managedInvoke(t, r, "click", managedClick("token"))
}

func TestManagedRunCloseClearsPendingForegroundOwnership(t *testing.T) {
	t.Parallel()
	m, r, f := managedTestRun(t, "darwin", ForegroundAllowed, false)
	if _, err := r.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	managedInvoke(t, r, "list_windows", `{}`)
	token := managedObserve(t, r, false)
	f.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name == "type_text" {
			return backgroundRefusal(), nil, true
		}
		return Reply{}, nil, false
	}
	args, _ := json.Marshal(map[string]any{"pid": 41, "window_id": 99, "element_token": token, "text": "task"})
	result, err := r.CallChecked(t.Context(), "type_text", args, nil)
	if err != nil || !result.IsError || len(r.managedRefusals) != 1 {
		t.Fatal("missing pending refusal", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(r.managedRefusals) != 0 || len(m.latest) != 0 {
		t.Fatal("closed run retained foreground ownership")
	}
}
