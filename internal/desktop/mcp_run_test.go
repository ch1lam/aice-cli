package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
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

func (f *managedFake) Info() mcpclient.Info {
	return mcpclient.Info{Name: "cua-driver", Version: DriverVersion}
}

func TestManagedCatalogPreservesNativeParameters(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			_, run, driver := managedTestRun(t, platform, BackgroundOnly, true)
			catalog, err := run.Tools(t.Context())
			if err != nil || len(catalog.Items) != 11 {
				t.Fatal("catalog", err)
			}
			for _, tool := range catalog.Items {
				var got map[string]any
				if json.Unmarshal(tool.InputSchema, &got) != nil {
					t.Fatal("schema")
				}
				for _, source := range driver.catalog.Items {
					if source.Name != tool.Name {
						continue
					}
					var want map[string]any
					_ = json.Unmarshal(source.InputSchema, &want)
					delete(want["properties"].(map[string]any), "session")
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s lost native schema fields", tool.Name)
					}
				}
			}
			if driver.count("start_session") != 1 || driver.count("get_window_state") != 0 {
				t.Fatal("discovery side effects")
			}
			if err := run.Close(); err != nil || driver.count("end_session") != 1 {
				t.Fatal("cleanup", err)
			}
		})
	}
}

func TestManagedCallsPreserveNativeOptionsAndResultsAcrossRuns(t *testing.T) {
	manager, first, driver := managedTestRun(t, "darwin", ForegroundAllowed, true)
	if _, err := first.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Bind(t.Context(), RunOptions{Mode: ForegroundAllowed, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	// PID/window from a previous turn need no second local discovery admission.
	for _, run := range []*Run{first, second} {
		managedInvoke(t, run, "get_window_state", `{"pid":41,"window_id":99,"include_screenshot":false,"max_elements":2000,"max_depth":25,"timeout_ms":8000}`)
	}
	response := Reply{IsError: true, Structured: []byte(`{"degraded":true,"escalation":{"recommended":"foreground"}}`), Text: []string{"native detail"}}
	driver.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		return response, nil, name != "end_session"
	}
	for range 2 {
		result := managedInvoke(t, second, "click", `{"pid":41,"window_id":99,"x":1000,"y":0.5,"capture_id":"driver-capture","delivery_mode":"foreground"}`)
		if !result.IsError || !bytes.Equal(result.StructuredContent, response.Structured) || len(result.Content) != 1 || result.Content[0].Text != "native detail" {
			t.Fatal("result was reinterpreted")
		}
	}
	managedInvoke(t, second, "launch_app", `{"name":"Fixture","additional_arguments":["--fixture"],"urls":["https://example.test"]}`)
	for _, call := range driver.calls {
		if call.name == "get_window_state" && (call.args["max_elements"] != float64(2000) || call.args["max_image_dimension"] != nil) {
			t.Fatal("observation defaults overridden")
		}
		if call.name == "click" && (call.args["x"] != float64(1000) || call.args["y"] != 0.5 || call.args["capture_id"] != "driver-capture" || call.args["session"] != second.id) {
			t.Fatal("input changed")
		}
	}
	if driver.count("list_windows") != 0 || driver.count("list_apps") != 0 || driver.count("click") != 2 || !manager.Status().Connected {
		t.Fatal("implicit discovery, retry or domain-error retirement")
	}
}

func TestManagedHostPoliciesAndExplicitForeground(t *testing.T) {
	for _, mode := range []ControlMode{BackgroundOnly, ForegroundAllowed} {
		t.Run(string(mode), func(t *testing.T) {
			_, run, driver := managedTestRun(t, "darwin", mode, false)
			if _, err := run.Tools(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, args := range []string{`{"pid":41,"window_id":99,"x":1,"y":2,"delivery_mode":"foreground"}`, `{"scope":"desktop","x":1,"y":2}`, `{"target":{"kind":"desktop","display_id":"primary"},"x":1,"y":2}`} {
				result, err := run.CallChecked(t.Context(), "click", []byte(args), nil)
				if mode == BackgroundOnly {
					if err == nil || result.State != llm.ExecutionNotDispatched {
						t.Fatal("background policy bypass")
					}
				} else if err != nil || result.State != llm.ExecutionReturned {
					t.Fatal("foreground required a local refusal", err)
				}
			}
			for _, args := range []string{`{"session":"foreign"}`, `{"pid":41,"pid":42}`, `{} {}`, `null`} {
				if _, err := run.CallChecked(t.Context(), "click", []byte(args), nil); err == nil {
					t.Fatal("ambiguous arguments accepted")
				}
			}
			for _, args := range []string{`{"pid":41,"window_id":99}`, `{"pid":41,"window_id":99,"include_screenshot":false,"screenshot_out_file":"/tmp/image.png"}`} {
				if _, err := run.CallChecked(t.Context(), "get_window_state", []byte(args), nil); err == nil {
					t.Fatal("image capability bypass")
				}
			}
			if _, err := run.CallChecked(t.Context(), "start_session", []byte(`{}`), nil); err == nil {
				t.Fatal("host lifecycle exposed")
			}
			if driver.count("get_window_state") != 0 {
				t.Fatal("rejected capture dispatched")
			}
		})
	}
}

func TestManagedFinalPermitAndUnknownNeverReplay(t *testing.T) {
	manager, run, driver := managedTestRun(t, "darwin", BackgroundOnly, false)
	if _, err := run.CallChecked(t.Context(), "list_windows", []byte(`{}`), nil); err == nil || driver.count("start_session") != 0 {
		t.Fatal("call connected implicitly")
	}
	if _, err := run.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("revoked")
	checks := 0
	result, err := run.CallChecked(t.Context(), "click", []byte(`{"element_token":"native-token"}`), func(context.Context) error {
		checks++
		if checks == 2 {
			return denied
		}
		return nil
	})
	if !errors.Is(err, denied) || result.State != llm.ExecutionNotDispatched || driver.count("click") != 0 {
		t.Fatal("final permit missing", err)
	}
	driver.handle = func(_ context.Context, name string, _ map[string]any) (Reply, error, bool) {
		return Reply{}, io.EOF, name == "click"
	}
	result, err = run.CallChecked(t.Context(), "click", []byte(`{"element_token":"native-token"}`), nil)
	if err == nil || result.State != llm.ExecutionUnknown || manager.Status().Connected || driver.count("click") != 1 {
		t.Fatal("unknown outcome lost or replayed", err)
	}
	if _, err := run.CallChecked(t.Context(), "click", []byte(`{"element_token":"native-token"}`), nil); err == nil {
		t.Fatal("silently reconnected")
	}
	if _, err := run.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	if driver.count("click") != 1 || driver.count("start_session") != 2 {
		t.Fatal("discovery replayed input")
	}
}

func TestManagedCloseCancelsRunningAndQueuedCalls(t *testing.T) {
	manager, run, driver := managedTestRun(t, "darwin", BackgroundOnly, false)
	if _, err := run.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	driver.handle = func(ctx context.Context, name string, _ map[string]any) (Reply, error, bool) {
		if name != "click" {
			return Reply{}, nil, false
		}
		close(entered)
		<-ctx.Done()
		return Reply{}, ctx.Err(), true
	}
	done := make(chan mcpclient.Result, 2)
	go func() {
		result, _ := run.CallChecked(t.Context(), "click", []byte(`{"element_token":"native-token"}`), nil)
		done <- result
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not start")
	}
	go func() {
		result, _ := run.CallChecked(t.Context(), "click", []byte(`{"element_token":"native-token"}`), nil)
		done <- result
	}()
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	states := map[llm.ExecutionState]int{}
	for range 2 {
		select {
		case result := <-done:
			states[result.State]++
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation did not settle")
		}
	}
	if states[llm.ExecutionUnknown] != 1 || states[llm.ExecutionNotDispatched] != 1 || driver.count("click") != 1 || manager.Status().Connected {
		t.Fatal("cancel replayed or admitted queued input", states)
	}
}

func TestManagedInfoAndCatalogInvalidationHaveNoSideEffects(t *testing.T) {
	_, run, driver := managedTestRun(t, "darwin", BackgroundOnly, false)
	if _, err := run.ServerInfo(t.Context()); err == nil || driver.count("start_session") != 0 {
		t.Fatal("info created lifecycle")
	}
	catalog, err := run.Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := len(driver.calls)
	info, err := run.ServerInfo(t.Context())
	if err != nil || info.Name != "cua-driver" || info.Version != DriverVersion || len(driver.calls) != before {
		t.Fatal("cached info", err)
	}
	driver.generation.Add(1)
	if run.ToolGeneration() == catalog.Generation {
		t.Fatal("stale generation remained executable")
	}
	if _, err := run.ServerInfo(t.Context()); err == nil {
		t.Fatal("stale metadata admitted")
	}
	result, err := run.CallChecked(t.Context(), "click", []byte(`{"element_token":"native-token"}`), nil)
	if err == nil || result.State != llm.ExecutionNotDispatched || driver.count("click") != 0 {
		t.Fatal("stale catalog executed")
	}
}

func TestManagedCallsUseAdmittedMCPWire(t *testing.T) {
	config, peer := fakeTransport(t, "managed-run")
	manager := newManager(func(ctx context.Context) (driverClient, error) { return connect(ctx, config) })
	defer manager.Close()
	run, err := manager.Bind(t.Context(), RunOptions{Mode: BackgroundOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	if _, err := run.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	token := managedObserve(t, run, false)
	managedInvoke(t, run, "click", managedClick(token))
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	if peer.initializes.Load() != 1 || peer.pages.Load() != 2 || peer.calls.Load() != 4 {
		t.Fatal("wire calls replayed or used a second connection", peer.calls.Load())
	}
}

func TestManagedSessionExpiryRetiresWithoutReplayAndReadmits(t *testing.T) {
	for _, name := range []string{"list_windows", "click"} {
		for _, form := range []string{"code", "refusal", "daemon"} {
			t.Run(name+"/"+form, func(t *testing.T) {
				manager, run, driver := managedTestRun(t, "darwin", BackgroundOnly, false)
				if _, err := run.Tools(t.Context()); err != nil {
					t.Fatal(err)
				}
				oldSession := run.id
				reply := Reply{IsError: true}
				switch form {
				case "code":
					reply.Structured = []byte(`{"code":"session_ended"}`)
				case "refusal":
					reply.Structured = []byte(`{"refusal":{"code":"session_ended"}}`)
				case "daemon":
					reply.Structured = []byte(`{"code":"tool_invocation_failed"}`)
					reply.Text = []string{"session 'native-implicit' has ended; tool call '" + name + "' was rejected. Call start_session with this id to revive it before issuing further actions, or use a new session id."}
				}
				driver.handle = func(_ context.Context, method string, _ map[string]any) (Reply, error, bool) {
					return reply, nil, method == name
				}
				result, err := run.CallChecked(t.Context(), name, []byte(`{}`), nil)
				if err != nil || !result.IsError || result.State != llm.ExecutionReturned || !bytes.Equal(result.StructuredContent, reply.Structured) {
					t.Fatal("expiry result changed", err)
				}
				if manager.Status().Connected || run.active || driver.count(name) != 1 || driver.count("start_session") != 1 {
					t.Fatal("expiry did not retire, or retried", manager.Status())
				}
				if _, err := run.CallChecked(t.Context(), name, []byte(`{}`), nil); err == nil || driver.count(name) != 1 {
					t.Fatal("expired connection executed")
				}
				driver.handle = nil
				if _, err := run.Tools(t.Context()); err != nil {
					t.Fatal("explicit rediscovery could not recover", err)
				}
				if run.id == oldSession || manager.Status().Generation != 2 || driver.count("start_session") != 2 || driver.count(name) != 1 {
					t.Fatal("recovery reused or replayed the expired lifecycle")
				}
				managedObserve(t, run, false)
			})
		}
	}
}

func TestManagedExpiryRequiresExactNativeFailure(t *testing.T) {
	exact := "session 'implicit' has ended; tool call 'list_windows' was rejected. Call start_session with this id to revive it before issuing further actions, or use a new session id."
	for _, result := range []mcpclient.Result{
		{StructuredContent: []byte(`{"code":"session_ended"}`)},
		{IsError: true, StructuredContent: []byte(`{"code":"background_unavailable"}`)},
		{IsError: true, StructuredContent: []byte(`{"degraded":true}`)},
		{IsError: true, Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "document says session_ended"}}},
		{IsError: true, Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: exact}}}, // another tool's diagnostic
	} {
		if managedSessionEnded("click", result) {
			t.Fatal("ordinary domain result was classified as lifecycle expiry")
		}
	}
}
