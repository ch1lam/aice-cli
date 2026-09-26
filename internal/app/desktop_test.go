package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type appDesktopBackend struct {
	calls  int
	result desktop.ActResult
}

func (b *appDesktopBackend) Apps(context.Context, string, int) (desktop.Discovery, error) {
	b.calls++
	return desktop.Discovery{}, nil
}
func (b *appDesktopBackend) Observe(context.Context, desktop.ObserveRequest) (desktop.Observation, error) {
	b.calls++
	return desktop.Observation{}, nil
}
func (b *appDesktopBackend) Act(context.Context, desktop.ActRequest) (desktop.ActResult, error) {
	b.calls++
	return b.result, nil
}

func TestDesktopRunBindingFreezesCapabilitiesAndRevokesOnClose(t *testing.T) {
	t.Parallel()
	var options []desktop.RunOptions
	closed := 0
	d := &desktopState{bind: func(ctx context.Context, o desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
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

func TestDesktopGuardRequiresActiveBindingEvenWithYolo(t *testing.T) {
	t.Parallel()
	g, adapter, err := newExecutionGuard(t.TempDir(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	d := &desktopState{bind: func(context.Context, desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
		return &appDesktopBackend{}, func() error { return nil }, nil
	}}
	adapter.desktop = d
	g.SetDesktopEnabled(true)
	call := llm.ToolCall{Name: "desktop_apps", Arguments: []byte(`{}`)}
	got, err := adapter.Check(t.Context(), call)
	if err != nil || got.Decision != agent.GuardDeny {
		t.Fatalf("unbound=%+v %v", got, err)
	}
	ctx, closeRun, err := d.bindContext(t.Context(), config.Config{DesktopEnabled: true}, llm.Model{})
	if err != nil {
		t.Fatal(err)
	}
	got, err = adapter.Check(ctx, call)
	if err != nil || got.Decision != agent.GuardAllow {
		t.Fatalf("bound=%+v %v", got, err)
	}
	g.SetDesktopEnabled(false)
	got, err = adapter.Check(ctx, call)
	if err != nil || got.Decision != agent.GuardDeny {
		t.Fatalf("disabled=%+v %v", got, err)
	}
	g.SetDesktopEnabled(true)
	_ = closeRun()
	got, err = adapter.Check(ctx, call)
	if err != nil || got.Decision != agent.GuardDeny {
		t.Fatalf("closed=%+v %v", got, err)
	}
}

func TestDesktopSettingsPublicationPreservesOtherTools(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), nil)
	s.desktop = &desktopState{}
	s.guardAdapter.desktop = s.desktop
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
	for _, name := range []string{"read", "web_fetch", "desktop_apps", "desktop_observe", "desktop_act"} {
		if !slicesContains(toolNames(s.tools), name) || !strings.Contains(s.systemPrompt, name+":") {
			t.Fatalf("missing %s after desktop publication", name)
		}
	}
	if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err != nil {
		t.Fatal(err)
	}
	if slicesContains(toolNames(s.tools), "web_fetch") || !slicesContains(toolNames(s.tools), "desktop_act") || !strings.Contains(s.systemPrompt, "desktop_act:") {
		t.Fatal("Web rebuild lost desktop capability")
	}
	change.Value.Bool = false
	snapshot, err := s.ReadSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Revision: snapshot.Revision, Changes: []interaction.SettingChange{change}})
	if err != nil || !result.Applied || slicesContains(toolNames(s.tools), "desktop_act") || strings.Contains(s.systemPrompt, "desktop_act:") {
		t.Fatalf("disable=%+v %v", result, err)
	}
}

func TestDesktopPrintUsesRealLoopAndClosesBinding(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			backend := &appDesktopBackend{}
			binds, closes, managers := 0, 0, 0
			model := &toolLoopModel{firstCall: &llm.ToolCall{ID: "desktop-call", Name: "desktop_apps", Arguments: []byte(`{}`)}}
			command, err := newTestCommand(t, dependencies{
				loadConfig: func(config.LoadOptions) (config.Config, error) {
					return config.Config{DeepSeekAPIKey: "test-key", DesktopEnabled: enabled}, nil
				},
				newModel: func(config.Config) (llm.Streamer, error) { return model, nil },
				newDesktop: func(config.Config) (*desktopState, error) {
					return &desktopState{
						bind: func(context.Context, desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
							binds++
							return backend, func() error { closes++; return nil }, nil
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
			if len(model.requests) != 2 {
				t.Fatalf("requests=%d", len(model.requests))
			}
			found := false
			for _, def := range model.requests[0].Tools {
				if def.Name == "desktop_apps" {
					found = true
				}
			}
			if found != enabled {
				t.Fatal("tool publication differs from enabled setting")
			}
		})
	}
}

func TestDesktopInteractivePersistsPartialActionImage(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), map[string]any{"provider": "deepseek"})
	img := &llm.ImageContent{Data: []byte("view"), MIMEType: "image/png", Original: &llm.ImageOriginal{Data: []byte("original"), MIMEType: "image/png"}}
	backend := &appDesktopBackend{result: desktop.ActResult{Dispatched: true, Outcome: "unknown", Observation: &desktop.Observation{Ref: "fresh", Image: img}, ObservationError: "partial observation"}}
	binds, closes := 0, 0
	s.desktop = &desktopState{bind: func(context.Context, desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
		binds++
		return backend, func() error { closes++; return nil }, nil
	}}
	s.guardAdapter.desktop = s.desktop
	model := &toolLoopModel{firstCall: &llm.ToolCall{ID: "desktop-action", Name: "desktop_act", Arguments: []byte(`{"action":"click","observation_ref":"synthetic","element_token":"token"}`)}}
	s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return model, nil }
	_, err := s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: true}}}})
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
	for _, fact := range []string{`unknown`, `partial observation`, `b3JpZ2luYWw=`, `"is_error":true`} {
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
