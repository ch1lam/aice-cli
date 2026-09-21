package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/skill"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

// webCommandSession builds an idle interactive session backed by temporary
// configuration files and fake web backends, without a TUI or model service.
func webCommandSession(t *testing.T, backends *webBackends, settingsFile map[string]any) *interactiveSession {
	t.Helper()
	paths := authTestPaths(t)
	if settingsFile != nil {
		data, err := json.Marshal(settingsFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.GlobalSettings, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("EXA_API_KEY", "")
	configuration, err := config.LoadFiles(paths, config.LoadOptions{Environment: true})
	if err != nil {
		t.Fatal(err)
	}
	configuration.DeepSeekAPIKey = "test-key"
	configuration.Provider = "deepseek"
	workspace, err := tool.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g, adapter, err := newExecutionGuard(workspace.PhysicalPath(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{dependencies: dependencies{
		saveWebSettings: config.SaveWebSettingsFile,
		webBackends:     backends,
		providers:       defaultProviders(),
		newModel:        func(config.Config) (llm.Streamer, error) { return &toolLoopModel{}, nil },
	}}
	base := testBuiltInTools(t, workspace)
	state := bindWeb(*backends, configuration.Web)
	g.SetSearchTarget(state.searchTarget)
	tools := append(append([]agent.Tool(nil), base...), state.tools()...)
	prompt, err := assembleSystemPrompt(workspace, configuration, trust.DecisionUntrusted, tools, skill.Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	s := &interactiveSession{
		application: app, guard: g, guardAdapter: adapter, guardRequests: make(chan interaction.GuardRequest, 1),
		configuration: configuration, tools: tools, baseTools: base, web: state, systemPrompt: prompt,
		workspace: workspace, workspacePath: workspace.PhysicalPath(), trustDecision: trust.DecisionUntrusted,
		providers: defaultProviders(), skills: skill.Catalog{},
	}
	loop, err := app.newAgentLoopWithOptions(configuration, tools, agent.WithGuard(adapter))
	if err != nil {
		t.Fatal(err)
	}
	s.loop = loop
	return s
}

// scriptedUI answers /web prompts in order and records every prompt.
type scriptedUI struct {
	prompts []interaction.AuthPrompt
	input   chan string
}

func newScriptedUI(answers ...string) (*scriptedUI, *interaction.AuthInteraction) {
	ui := &scriptedUI{input: make(chan string, len(answers))}
	for _, answer := range answers {
		ui.input <- answer
	}
	return ui, &interaction.AuthInteraction{Input: ui.input, Notify: func(_ context.Context, prompt interaction.AuthPrompt) error {
		ui.prompts = append(ui.prompts, prompt)
		return nil
	}}
}

func TestWebCommandAddsInstanceSavesCredentialAndRebinds(t *testing.T) {
	search := &fakeWireBackend{}
	backends := testWebBackends(search, &fakeFetch{})
	// Register the production Exa factory name so the TUI flow (which adds
	// provider "exa") binds through the fake backend without network access.
	backends.search["exa"] = fakeSearchFactory(search, "exa-rest")
	s := webCommandSession(t, backends, nil)
	if toolNames(s.tools)[len(s.tools)-1] != "web_fetch" || slicesContains(toolNames(s.tools), "web_search") {
		t.Fatalf("initial tools = %v", toolNames(s.tools))
	}
	status, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web"})
	if err != nil || !strings.Contains(status, "Search source: none") || !strings.Contains(status, "native: not_implemented") {
		t.Fatalf("status = %q %v", status, err)
	}

	ui, auth := newScriptedUI("key", " secret-exa-key ")
	output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "add", Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Added Exa instance exa-main") || !strings.Contains(output, "Search source: service:exa-main") || strings.Contains(output, "secret-exa-key") {
		t.Fatalf("output = %q", output)
	}
	if len(ui.prompts) != 2 || ui.prompts[0].Menu == nil || !ui.prompts[1].AllowInput || !strings.Contains(ui.prompts[1].InputLabel, "hidden") {
		t.Fatalf("prompts = %+v", ui.prompts)
	}
	paths := s.configuration.Paths
	settingsData, _ := os.ReadFile(paths.GlobalSettings)
	authData, _ := os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(settingsData), "secret-exa-key") || !strings.Contains(string(authData), `"exa-main": "secret-exa-key"`) {
		t.Fatalf("settings=%s auth=%s", settingsData, authData)
	}
	if !strings.Contains(string(settingsData), `"auth_ref": "web_services.exa-main"`) || !strings.Contains(string(settingsData), `"service:exa-main"`) {
		t.Fatalf("settings = %s", settingsData)
	}
	if got := s.settingsSnapshot().configuration.Web.Priority; !reflect.DeepEqual(got, []string{"native", "service:exa-main"}) {
		t.Fatalf("priority = %v", got)
	}
	if !slicesContains(toolNames(s.tools), "web_search") || !strings.Contains(s.systemPrompt, "web_search:") {
		t.Fatalf("tools not rebound: %v", toolNames(s.tools))
	}
	if !strings.Contains(s.settingsInformation(), "Web: search on (source service:exa-main), fetch on") {
		t.Fatalf("settings summary = %q", s.settingsInformation())
	}
	// The Guard now knows the bound fingerprint.
	res, err := s.guard.Check(t.Context(), llm.ToolCall{ID: "c", Name: "web_search", Arguments: json.RawMessage(`{"query":"x"}`)})
	if err != nil || res.Decision != guard.DecisionAsk || !strings.Contains(res.Approvals[0].Action.Target, "exa-main@https://fake.example") {
		t.Fatalf("guard = %+v %v", res, err)
	}

	// Reload from disk resolves the stored credential.
	t.Setenv("EXA_API_KEY", "")
	reloaded, err := config.LoadFiles(paths, config.LoadOptions{Environment: true})
	if err != nil || !reloaded.Web.Services["exa-main"].SecretPresent || reloaded.Web.Services["exa-main"].Secret != "secret-exa-key" {
		t.Fatalf("reloaded = %+v %v", reloaded.Web, err)
	}

	// Reordering, toggles and removal.
	_, auth = newScriptedUI("service:exa-main")
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "up", Auth: auth}); err != nil || !strings.Contains(output, "Priority: service:exa-main, native") {
		t.Fatalf("up = %q %v", output, err)
	}
	_, auth = newScriptedUI("native")
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "drop", Auth: auth}); err != nil || !strings.Contains(output, "Priority: service:exa-main\n") {
		t.Fatalf("drop = %q %v", output, err)
	}
	_, auth = newScriptedUI("native")
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "append", Auth: auth}); err != nil || !strings.Contains(output, "Priority: service:exa-main, native") {
		t.Fatalf("append = %q %v", output, err)
	}
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err != nil || !strings.Contains(output, "Web fetch: off") || slicesContains(toolNames(s.tools), "web_fetch") {
		t.Fatalf("fetch toggle = %q %v tools=%v", output, err, toolNames(s.tools))
	}
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "search"}); err != nil || !strings.Contains(output, "Web search: off") || slicesContains(toolNames(s.tools), "web_search") {
		t.Fatalf("search toggle = %q %v", output, err)
	}
	if s.guard.Workspace() == "" {
		t.Fatal("guard lost")
	}
	if res, _ := s.guard.Check(t.Context(), llm.ToolCall{ID: "c", Name: "web_search", Arguments: json.RawMessage(`{"query":"x"}`)}); res.Decision != guard.DecisionDeny {
		t.Fatalf("unbound guard = %+v", res)
	}
	_, auth = newScriptedUI("exa-main")
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "remove", Auth: auth}); err != nil || !strings.Contains(output, "Removed instance exa-main") || !strings.Contains(output, "Stored credential removed") {
		t.Fatalf("remove = %q %v", output, err)
	}
	authData, _ = os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(authData), "secret-exa-key") {
		t.Fatalf("credential survived removal: %s", authData)
	}
	if got := s.settingsSnapshot().configuration.Web.Priority; !reflect.DeepEqual(got, []string{"native"}) {
		t.Fatalf("priority after removal = %v", got)
	}
	if search.calls.Load() != 0 {
		t.Fatal("settings changes contacted the search backend")
	}
}

func TestWebCommandFailedSaveKeepsSnapshotAndReportsCredential(t *testing.T) {
	search := &fakeWireBackend{}
	backends := testWebBackends(search, &fakeFetch{})
	backends.search["exa"] = fakeSearchFactory(search, "exa-rest")
	s := webCommandSession(t, backends, map[string]any{"web": map[string]any{"search": map[string]any{"priority": []string{"native"}}}})
	before := s.settingsSnapshot().configuration.Web
	// Corrupt the settings file so the patch fails after the credential is stored.
	if err := os.WriteFile(s.configuration.Paths.GlobalSettings, []byte(`{"web":{"search":{"bogus":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, auth := newScriptedUI("key", "new-secret")
	_, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "add", Auth: auth})
	if err == nil || !strings.Contains(err.Error(), "credential saved to") || !strings.Contains(err.Error(), "current Session is unchanged") {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(s.settingsSnapshot().configuration.Web.Priority, before.Priority) || slicesContains(toolNames(s.tools), "web_search") {
		t.Fatal("failed save changed the live snapshot")
	}
	authData, _ := os.ReadFile(s.configuration.Paths.GlobalAuth)
	if !strings.Contains(string(authData), "new-secret") {
		t.Fatal("credential-only success not persisted")
	}
	// Changes are refused while a response is running.
	s.conversation.activeMainRun = &mainRunState{}
	if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "fetch"}); err == nil || !strings.Contains(err.Error(), "while a response is running") {
		t.Fatalf("active run allowed change: %v", err)
	}
	if output, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "status"}); err != nil || !strings.Contains(output, "Web") {
		t.Fatalf("status during run = %q %v", output, err)
	}
	if _, err := s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "web", Arguments: "add"}); err == nil {
		t.Fatal("add without interactive UI accepted")
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
