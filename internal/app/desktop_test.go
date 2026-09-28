package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type appDesktopBackend struct {
	calls         int
	managedResult mcpclient.Result
}

func (*appDesktopBackend) ControlMode() desktop.ControlMode { return desktop.BackgroundOnly }
func (*appDesktopBackend) ToolGeneration() uint64           { return 1 }
func (*appDesktopBackend) Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	return mcpclient.Catalog[mcpclient.Tool]{Complete: true, Generation: 1, Items: []mcpclient.Tool{catalogFixtureTool("list_windows", "Discover windows"), catalogFixtureTool("click", "Click window")}}, nil
}
func (b *appDesktopBackend) CallChecked(ctx context.Context, _ string, _ json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if err := check(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	b.calls++
	if b.managedResult.State != "" {
		return b.managedResult, nil
	}
	return mcpclient.Result{State: llm.ExecutionReturned, Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "returned"}}}, nil
}

type managedDesktopModel struct {
	arguments json.RawMessage
	operation string
	requests  []llm.Request
}

func (m *managedDesktopModel) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	m.requests = append(m.requests, req)
	if len(m.requests) == 1 {
		raw, _ := json.Marshal(tool.ToolSearchRequest{Service: managedCUAKey, Query: m.operation, Limit: 1})
		return toolCallEventStream(req.Model, llm.ToolCall{ID: "discover", Name: "tool_search", Arguments: raw}), nil
	}
	if len(m.requests) == 2 {
		result, ok := req.Messages[len(req.Messages)-1].(llm.ToolResultMessage)
		if !ok {
			return nil, fmt.Errorf("missing discovery result")
		}
		if !result.IsError {
			var found tool.ToolSearchResult
			if len(result.Content) == 0 || json.Unmarshal([]byte(result.Content[0].Text), &found) != nil || len(found.Entries) != 1 {
				return nil, fmt.Errorf("missing selected managed tool")
			}
			args := m.arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			return toolCallEventStream(req.Model, llm.ToolCall{ID: "execute", Name: found.Entries[0].Name, Arguments: args}), nil
		}
	}
	return (&recordingModel{response: "finished"}).Stream(ctx, req)
}

func TestDesktopRunBindingFreezesCapabilitiesAndRevokesOnClose(t *testing.T) {
	t.Parallel()
	var options []desktop.RunOptions
	closed := 0
	d := &desktopState{bind: func(ctx context.Context, o desktop.RunOptions) (managedDesktopRun, func() error, error) {
		options = append(options, o)
		return &appDesktopBackend{}, func() error { closed++; return nil }, nil
	}}
	cfg := config.Config{DesktopEnabled: true, DesktopControlMode: "background_only"}
	first, closeFirst, err := d.bindContext(t.Context(), cfg, llm.Model{InputModalities: []llm.InputModality{llm.InputModalityImage}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DesktopControlMode = "foreground_allowed"
	second, closeSecond, err := d.bindContext(t.Context(), cfg, llm.Model{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSecond()
	if !reflect.DeepEqual(options, []desktop.RunOptions{{Mode: desktop.BackgroundOnly, Images: true}, {Mode: desktop.ForegroundAllowed}}) {
		t.Fatalf("options=%+v", options)
	}
	if _, err := (&desktopState{}).bound(first); err == nil {
		t.Fatal("another owner accepted binding")
	}
	if err := closeFirst(); err != nil {
		t.Fatal(err)
	}
	_ = closeFirst()
	if _, err := d.bound(first); !errors.Is(err, context.Canceled) || closed != 1 {
		t.Fatalf("closed binding err=%v closes=%d", err, closed)
	}
	if _, err := d.bound(second); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopSettingsPublicationPreservesOtherTools(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), nil)
	s.desktop = &desktopState{}
	oldTools, oldPrompt, oldLoop := toolNames(s.tools), s.systemPrompt, s.loop
	change := interaction.SettingChange{ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: true}}
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"broken":`)
	result, err := s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{change}})
	if err == nil || result.Committed || s.configuration.DesktopEnabled || !reflect.DeepEqual(toolNames(s.tools), oldTools) || s.systemPrompt != oldPrompt || s.loop != oldLoop {
		t.Fatal("failed save published desktop candidate")
	}
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{}`)
	result, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{change}})
	if err != nil || !result.Committed || !result.Applied {
		t.Fatalf("enable=%+v %v", result, err)
	}
	for _, name := range []string{"read", "web_fetch", "tool_search", "mcp_resource_list", "mcp_server_info"} {
		if !slicesContains(toolNames(s.tools), name) || (name == "read" || name == "web_fetch") && !strings.Contains(s.systemPrompt, name+":") {
			t.Fatalf("missing %s after desktop publication", name)
		}
	}
	if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err != nil {
		t.Fatal(err)
	}
	if slicesContains(toolNames(s.tools), "web_fetch") || !slicesContains(toolNames(s.tools), "tool_search") {
		t.Fatal("Web rebuild lost desktop capability")
	}
	change.Value.Bool = false
	snapshot, err := s.ReadSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Revision: snapshot.Revision, Changes: []interaction.SettingChange{change}})
	if err != nil || !result.Applied || slicesContains(toolNames(s.tools), "tool_search") || strings.Contains(s.systemPrompt, "tool_search:") {
		t.Fatalf("disable=%+v %v", result, err)
	}
}

func TestDesktopPrintUsesRealLoopAndClosesBinding(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			backend := &appDesktopBackend{}
			binds, closes, managers := 0, 0, 0
			model := &managedDesktopModel{operation: "list_windows"}
			command, err := newTestCommand(t, dependencies{
				loadConfig: func(config.LoadOptions) (config.Config, error) {
					return config.Config{DeepSeekAPIKey: "test-key", DesktopEnabled: enabled}, nil
				},
				newModel: func(config.Config) (llm.Streamer, error) { return model, nil },
				newDesktop: func(config.Config) (*desktopState, error) {
					return &desktopState{
						bind: func(context.Context, desktop.RunOptions) (managedDesktopRun, func() error, error) {
							binds++
							// Expose only MCP methods: the real Print path must
							// not depend on the legacy typed action interface.
							return struct{ managedDesktopRun }{backend}, func() error { closes++; return nil }, nil
						},
						close: func() error { managers++; return nil },
					}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			command.SetOut(new(bytes.Buffer))
			command.SetErr(new(bytes.Buffer))
			command.SetArgs([]string{"--workspace", t.TempDir(), "--print", "inspect synthetic desktop"})
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := 0
			if enabled {
				want = 1
			}
			if backend.calls != want || binds != want || closes != want || managers != 1 {
				t.Fatalf("calls=%d binds=%d closes=%d managers=%d", backend.calls, binds, closes, managers)
			}
			if len(model.requests) != 2+want {
				t.Fatalf("requests=%d", len(model.requests))
			}
			found := false
			for _, def := range model.requests[0].Tools {
				if desktopTool(def.Name) {
					t.Fatal("initial request exposed an eager or legacy desktop action")
				}
				if def.Name == "tool_search" {
					found = true
				}
			}
			if found != enabled {
				t.Fatal("tool publication differs from enabled setting")
			}
			if strings.Contains(model.requests[0].SystemPrompt, "managed:cua: Computer Use enabled") != enabled {
				t.Fatal("managed summary differs from enabled setting")
			}
			for _, request := range model.requests {
				for _, def := range request.Tools {
					if def.Name == "desktop_apps" || def.Name == "desktop_observe" || def.Name == "desktop_act" {
						t.Fatal("managed and typed routes were exposed together")
					}
				}
			}
		})
	}
}

func TestDesktopInteractivePersistsPartialActionImage(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), map[string]any{"provider": "deepseek"})
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	backend := &appDesktopBackend{managedResult: mcpclient.Result{State: llm.ExecutionUnknown, IsError: true, StructuredContent: []byte(`{"partial":"partial observation"}`), Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "partial observation"}, {Kind: mcpclient.BlockImage, Data: pngData.Bytes(), MIMEType: "image/png"}}}}
	var err error
	s.mcp, err = newMCPOwner(s.configuration.MCP, s.guard, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.mcp.Close()
	s.guardAdapter.mcp = mcpRunRouter{}

	binds, closes := 0, 0
	s.desktop = &desktopState{bind: func(context.Context, desktop.RunOptions) (managedDesktopRun, func() error, error) {
		binds++
		return backend, func() error { closes++; return nil }, nil
	}}
	model := &managedDesktopModel{operation: "click"}
	s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return model, nil }
	_, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if binds != 0 {
		t.Fatal("Settings touched native run")
	}
	var displayed []interaction.Event
	active, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: "synthetic action"}, func(_ context.Context, event interaction.Event) error {
		displayed = append(displayed, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.conversation.store.Close()
	if binds != 0 {
		t.Fatal("preparation bound native run")
	}
	if err := active.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if binds != 1 || closes != 1 || backend.calls != 1 {
		t.Fatalf("binds=%d closes=%d calls=%d", binds, closes, backend.calls)
	}
	snapshot, err := s.conversation.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, event := range displayed {
		if event.Tool.Desktop != nil {
			phases = append(phases, event.Tool.Desktop.Phase)
		}
	}
	if !reflect.DeepEqual(phases, []string{"Background requested", "Outcome unknown"}) {
		t.Fatalf("live desktop projection: %v", phases)
	}
	transcript, err := sessionTranscript(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range transcript.Entries {
		if entry.Tool.Desktop != nil {
			found = true
			if entry.Tool.Desktop.Phase != "Outcome unknown" {
				t.Fatal("replay lost unknown outcome")
			}
		}
	}
	if !found {
		t.Fatal("replay lost desktop presentation")
	}
	data, err := json.Marshal(snapshot.Messages)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{`unknown`, `partial observation`, base64.StdEncoding.EncodeToString(pngData.Bytes()), `"is_error":true`} {
		if !bytes.Contains(data, []byte(fact)) {
			t.Fatalf("Session lost %q: %s", fact, data)
		}
	}
}

func TestDesktopDisabledConstructionDoesNotRequireHome(t *testing.T) {
	t.Parallel()
	a := &application{dependencies: dependencies{userHomeDir: func() (string, error) { return "", errors.New("home unavailable") }}}
	d, err := a.newDesktopState(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.status().Connected {
		t.Fatal("constructor connected native service")
	}
}
