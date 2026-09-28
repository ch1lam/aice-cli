package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func mcpTestInput(t *testing.T, values ...string) *interaction.AuthInteraction {
	t.Helper()
	input := make(chan string, 1)
	return &interaction.AuthInteraction{Input: input, Notify: func(_ context.Context, prompt interaction.AuthPrompt) error {
		if len(values) == 0 {
			t.Errorf("unexpected prompt: %s", prompt.Title)
			return context.Canceled
		}
		input <- values[0]
		values = values[1:]
		return nil
	}}
}

func runMCPSettingsFixture(t *testing.T, definition string, run func(context.Context, *interactiveSession)) config.Paths {
	t.Helper()
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"fixture","mcp":{"servers":{"docs":`+definition+`}}}`)
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:   func(config.Config) (llm.Streamer, error) { return &recordingModel{response: "answer"}, nil },
		runTUI: func(ctx context.Context, runner interaction.Runner, _ tui.Options) error {
			run(ctx, runner.(*interactiveSession))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", t.TempDir(), "--no-approve", "--no-dep-install", "--no-update-check"})
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestMCPSettingsReconnectRevocationAndExit(t *testing.T) {
	endpoint, initialized, calls, closed := mcpStartupServer(t)
	runMCPSettingsFixture(t, `{"transport":"http","url":"`+endpoint+`"}`, func(ctx context.Context, s *interactiveSession) {
		act := func(action string, values ...string) (interaction.SettingsActionResult, error) {
			revision, _ := s.settingsStatus()
			return s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: action + " user:docs", Auth: mcpTestInput(t, values...)})
		}
		read, err := s.ReadSettings(ctx)
		if err != nil || initialized.Load() != 0 || s.conversation.store != nil {
			t.Fatal("settings performed I/O or created a Session", err)
		}
		found := false
		for _, field := range read.Fields {
			found = found || field.ID == "mcp.services"
		}
		if !found {
			t.Fatal("missing MCP Settings entry")
		}
		approved, err := act("approve", "confirm")
		if err != nil || !approved.Committed || !approved.Applied || initialized.Load() != 0 {
			t.Fatal("approval did not publish lazily", approved, err)
		}
		if _, err := s.RunSettingsAction(ctx, read.Revision, interaction.CommandRequest{Name: "mcp", Arguments: "status"}); !errors.Is(err, interaction.ErrSettingsStale) {
			t.Fatal("accepted stale panel", err)
		}
		if _, err := act("connect"); err != nil {
			t.Fatal(err)
		}
		_, oldRun, err := s.mcp.bindRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRun.Close()
		selected, err := oldRun.catalog.Search(ctx, tool.ToolSearchRequest{Service: "user:docs", Limit: 1})
		if err != nil || len(selected.Selected) != 1 {
			t.Fatal("no tool discovered", err)
		}
		resolved, _ := oldRun.catalog.Resolve(ctx, []string{selected.Selected[0].ID})
		binding, scope, _, _ := oldRun.catalog.MCPBinding(ctx, resolved[0].Tool.Definition().Name)
		_, permit, err := s.guard.CheckMCP(ctx, resolved[0].Tool.Definition().Name, binding, scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := permit.AllowSession(ctx, false); err != nil {
			t.Fatal(err)
		}
		if _, err := act("reconnect"); err != nil {
			t.Fatal(err)
		}
		if initialized.Load() != 2 || closed.Load() != 1 || oldRun.catalog.Check(ctx, selected.Selected[0]) == nil {
			t.Fatal("reconnect retained old transport or reference")
		}
		_, currentRun, err := s.mcp.bindRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer currentRun.Close()
		if _, err := currentRun.catalog.Search(ctx, tool.ToolSearchRequest{Service: "user:docs", Limit: 1}); err != nil {
			t.Fatal(err)
		}
		decision, _, err := s.guard.CheckMCP(ctx, resolved[0].Tool.Definition().Name, binding, scope)
		if err != nil || decision.Decision != guard.DecisionAsk {
			t.Fatal("reconnect retained Session grant", err)
		}
		s.lifecycle.mu.Lock()
		s.lifecycle.mainRunning = true
		s.lifecycle.mu.Unlock()
		if _, err := act("reconnect"); !errors.Is(err, interaction.ErrSettingsRunning) {
			t.Fatal("reconnected during run", err)
		}
		denied, err := act("deny", "confirm")
		if err != nil || !denied.Committed || !denied.Applied || closed.Load() != 2 {
			t.Fatal("live denial failed", denied, err)
		}
		s.lifecycle.mu.Lock()
		s.lifecycle.mainRunning = false
		s.lifecycle.mu.Unlock()
		_, revokedRun, err := s.mcp.bindRun(ctx)
		if err != nil {
			t.Fatal("optional revoked service blocked the next run", err)
		}
		defer revokedRun.Close()
		unavailable, err := revokedRun.catalog.Search(ctx, tool.ToolSearchRequest{Service: "user:docs", Limit: 1})
		if err != nil || len(unavailable.Selected) != 0 || initialized.Load() != 2 {
			t.Fatal("revoked catalog connected", err)
		}
		if _, err := act("approve", "confirm"); err != nil {
			t.Fatal(err)
		}
		if _, err := act("connect"); err != nil {
			t.Fatal(err)
		}
		if initialized.Load() != 3 || s.conversation.store != nil {
			t.Fatal("restoring approval failed or command created Session")
		}
	})
	if initialized.Load() != 3 || closed.Load() != 3 || calls.Load() != 0 {
		t.Fatalf("exit leaked replacement or management called tool: %d/%d/%d", initialized.Load(), closed.Load(), calls.Load())
	}
}

func TestMCPSettingsCredentialsPartialRemovalAndCancellation(t *testing.T) {
	runMCPSettingsFixture(t, `{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":{"auth_ref":"token","prefix":"Bearer "}}}`, func(ctx context.Context, s *interactiveSession) {
		act := func(action string, values ...string) (interaction.SettingsActionResult, error) {
			revision, _ := s.settingsStatus()
			return s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: action + " user:docs", Auth: mcpTestInput(t, values...)})
		}
		initial := s.mcp
		cancelled, err := act("approve", "cancel")
		if !errors.Is(err, context.Canceled) || cancelled.Committed || cancelled.Revision != 0 || s.mcp != initial {
			t.Fatal("cancel changed resources", err)
		}
		secret := "private-mcp-fixture-value"
		stored, err := act("credential", "token", "set", secret)
		if err != nil || !stored.Committed || !stored.Applied || strings.Contains(stored.Output, secret) {
			t.Fatal("credential operation", err)
		}
		if len(s.configuration.MCP.Servers["user:docs"].MissingValues) != 0 {
			t.Fatal("credential not published")
		}
		if _, err := act("approve", "confirm"); err != nil {
			t.Fatal(err)
		}
		old := s.mcp
		writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"broken":`)
		removed, err := act("remove", "confirm")
		if err == nil || !removed.Committed || !removed.Applied || s.mcp == old {
			t.Fatal("partial commit not applied", removed, err)
		}
		if s.configuration.MCP.ConnectionDecision("user:docs") != config.MCPConnectionAsk || len(s.configuration.MCP.Servers["user:docs"].MissingValues) == 0 {
			t.Fatal("partial removal retained access")
		}
		if s.conversation.store != nil {
			t.Fatal("private inputs entered Session")
		}
	})
}

func TestMCPSlashAddsAndRemovesSearchWithoutModelRun(t *testing.T) {
	runMCPSettingsFixture(t, `{"transport":"http","url":"https://example.com/mcp"}`, func(ctx context.Context, s *interactiveSession) {
		if _, err := s.RunSlashCommand(ctx, interaction.CommandRequest{Name: "mcp", Arguments: "remove user:docs", Auth: mcpTestInput(t, "confirm")}); err != nil {
			t.Fatal(err)
		}
		hasSearch := func() bool {
			for _, tool := range s.tools {
				if tool.Definition().Name == "tool_search" {
					return true
				}
			}
			return false
		}
		if hasSearch() {
			t.Fatal("removed service retained search definition")
		}
		if _, err := s.RunSlashCommand(ctx, interaction.CommandRequest{Name: "mcp", Arguments: "add", Auth: mcpTestInput(t, "newdocs", `{"transport":"http","url":"https://example.com/new"}`)}); err != nil {
			t.Fatal(err)
		}
		if !hasSearch() || s.configuration.MCP.ConnectionDecision("user:newdocs") != config.MCPConnectionAsk {
			t.Fatal("add did not publish new tool set")
		}
		if _, err := s.RunSlashCommand(ctx, interaction.CommandRequest{Name: "mcp", Arguments: "status"}); err != nil {
			t.Fatal(err)
		}
		if s.conversation.store != nil {
			t.Fatal("management started Session")
		}
	})
}

func TestMCPSettingsSavedPolicyFailsClosedOnRuntimeError(t *testing.T) {
	runMCPSettingsFixture(t, `{"transport":"http","url":"https://example.com/mcp"}`, func(ctx context.Context, s *interactiveSession) {
		old := s.mcp
		s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return nil, errors.New("fixture preparation failure") }
		revision, _ := s.settingsStatus()
		result, err := s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: "disable user:docs"})
		if err == nil || !result.Committed || result.Applied || s.configuration.MCP.ServerAllowed("user:docs") || s.loop != nil || s.modelErr == nil {
			t.Fatal("saved policy left older runtime executable", result, err)
		}
		if _, err := old.Connections()["user:docs"].Tools(ctx); err == nil {
			t.Fatal("failed publication retained old transport")
		}
		loaded, err := config.LoadFiles(s.configuration.Paths, config.LoadOptions{})
		if err != nil || loaded.MCP.ServerAllowed("user:docs") {
			t.Fatal("reported save was not durable", err)
		}
		s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil }
		result, err = s.RunSettingsAction(ctx, result.Revision, interaction.CommandRequest{Name: "mcp", Arguments: "enable user:docs"})
		if err != nil || !result.Applied || s.loop == nil || s.modelErr != nil {
			t.Fatal("explicit repair did not restore a consistent runtime", err)
		}
	})
}
