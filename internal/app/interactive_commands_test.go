package app

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInteractiveSessionSlashCommandsNavigateCurrentStore(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspacePath, sessionPath, "first prompt", "first answer")
	runPrintTurn(t, workspacePath, sessionPath, "second prompt", "second answer")

	store, snapshot := openInteractiveCommandStore(
		t,
		workspacePath,
		sessionPath,
	)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	history, err := sessionHistory(snapshot)
	if err != nil {
		t.Fatalf("sessionHistory() error = %v", err)
	}
	runner := &interactiveSession{
		store:   store,
		history: history,
	}

	info, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "session",
	})
	if err != nil {
		t.Fatalf("/session error = %v", err)
	}
	for _, want := range []string{
		snapshot.Header.ID,
		sessionPath,
		"Active leaf: " + snapshot.Turns[1].ID,
	} {
		if !strings.Contains(info, want) {
			t.Errorf("/session output = %q, want %q", info, want)
		}
	}

	tree, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "tree",
	})
	if err != nil {
		t.Fatalf("/tree error = %v", err)
	}
	if !strings.Contains(tree, "* turn "+snapshot.Turns[1].ID) {
		t.Fatalf("/tree output = %q, want active second turn", tree)
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "checkout",
		Arguments: snapshot.Turns[0].ID,
	})
	if err != nil {
		t.Fatalf("/checkout error = %v", err)
	}
	if !strings.Contains(output, "next turn will branch") {
		t.Errorf("/checkout output = %q, want branch guidance", output)
	}
	if len(runner.history) != 2 {
		t.Fatalf("history after checkout = %d messages, want first turn", len(runner.history))
	}
	assertInteractiveTextMessage(t, runner.history[0], llm.RoleUser, "first prompt")
	assertInteractiveTextMessage(t, runner.history[1], llm.RoleAssistant, "first answer")

	updated, err := store.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if updated.LeafID != snapshot.Turns[0].ID || len(updated.LeafMoves) != 1 {
		t.Fatalf("snapshot after checkout = %#v", updated)
	}
	checkout := interactiveSlashCommand(t, runner.SlashCommands(), "checkout")
	if checkout.Menu == nil {
		t.Fatal("checkout command has no selection menu")
	}
	currentFound := false
	for _, option := range checkout.Menu.Options {
		if option.Arguments == snapshot.Turns[0].ID && option.Current {
			currentFound = true
			break
		}
	}
	if !currentFound {
		t.Fatalf(
			"checkout menu does not mark the new active leaf: %#v",
			checkout.Menu.Options,
		)
	}
}

func TestInteractiveSessionSlashCommandsExposeSelectionMenus(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspacePath, sessionPath, "first prompt", "first answer")
	runPrintTurn(t, workspacePath, sessionPath, "second prompt", "second answer")
	store, snapshot := openInteractiveCommandStore(
		t,
		workspacePath,
		sessionPath,
	)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	selectedModel, exists := deepseekModel(deepseek.ModelV4Pro)
	if !exists {
		t.Fatal("DeepSeek V4 Pro test model is unavailable")
	}
	runner := &interactiveSession{
		store: store,
		model: selectedModel,
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelHigh,
		},
		configuration: config.Config{
			Provider: string(deepseek.ProviderID),
		},
	}

	commands := runner.SlashCommands()
	for _, name := range []string{
		"login",
		"provider",
		"model",
		"thinking",
		"checkout",
	} {
		command := interactiveSlashCommand(t, commands, name)
		if command.ArgumentHint != "" {
			t.Errorf("/%s still advertises arguments: %q", name, command.ArgumentHint)
		}
		if command.Menu == nil || len(command.Menu.Options) == 0 {
			t.Errorf("/%s has no selection menu: %#v", name, command)
		}
	}

	login := interactiveSlashCommand(t, commands, "login")
	if got := login.Menu.Options[0].Arguments; got != string(deepseek.ProviderID) {
		t.Errorf("login provider arguments = %q, want deepseek", got)
	}
	if login.Menu.Options[0].Menu != nil {
		t.Fatal("login provider unexpectedly opens a settings scope menu")
	}

	provider := interactiveSlashCommand(t, commands, "provider")
	providerOption := provider.Menu.Options[0]
	if got := providerOption.Arguments; got != string(deepseek.ProviderID) {
		t.Errorf("provider arguments = %q, want deepseek", got)
	}
	if providerOption.Menu != nil {
		t.Fatal("provider unexpectedly opens a settings scope menu")
	}

	model := interactiveSlashCommand(t, commands, "model")
	if got, want := len(model.Menu.Options), len(deepseek.Models()); got != want {
		t.Fatalf("model options = %d, want %d", got, want)
	}
	proFound := false
	for _, option := range model.Menu.Options {
		if option.Description != deepseek.ModelV4Pro {
			continue
		}
		proFound = true
		if !option.Current {
			t.Error("current model is not marked in the model menu")
		}
		if option.Arguments != deepseek.ModelV4Pro {
			t.Errorf("model arguments = %q, want V4 Pro", option.Arguments)
		}
		if option.Menu != nil {
			t.Error("model unexpectedly opens a settings scope menu")
		}
	}
	if !proFound {
		t.Fatalf("model menu = %#v, want V4 Pro", model.Menu.Options)
	}

	thinking := interactiveSlashCommand(t, commands, "thinking")
	highFound := false
	for _, option := range thinking.Menu.Options {
		if option.Label != "High" {
			continue
		}
		highFound = true
		if !option.Current {
			t.Error("current thinking level is not marked")
		}
		if option.Arguments != "high" {
			t.Errorf("thinking arguments = %q, want high", option.Arguments)
		}
		if option.Menu != nil {
			t.Error("thinking unexpectedly opens a settings scope menu")
		}
	}
	if !highFound {
		t.Fatalf("thinking menu = %#v, want High", thinking.Menu.Options)
	}

	checkout := interactiveSlashCommand(t, commands, "checkout")
	if got, want := len(checkout.Menu.Options), len(snapshot.Order)+1; got != want {
		t.Fatalf("checkout options = %d, want root plus %d nodes", got, len(snapshot.Order))
	}
	if checkout.Menu.Options[0].Arguments != "root" {
		t.Errorf("first checkout option = %#v, want Session root", checkout.Menu.Options[0])
	}
	activeFound := false
	for _, option := range checkout.Menu.Options {
		if option.Arguments == snapshot.LeafID && option.Current {
			activeFound = true
		}
	}
	if !activeFound {
		t.Fatalf("checkout menu does not mark active leaf %q", snapshot.LeafID)
	}
}

func TestInteractiveSessionSlashCommandCompactsAndReloadsHistory(
	t *testing.T,
) {
	t.Parallel()

	workspacePath := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspacePath, sessionPath, "first prompt", "first answer")
	runPrintTurn(t, workspacePath, sessionPath, "second prompt", "second answer")

	store, snapshot := openInteractiveCommandStore(
		t,
		workspacePath,
		sessionPath,
	)
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	history, err := sessionHistory(snapshot)
	if err != nil {
		t.Fatalf("sessionHistory() error = %v", err)
	}
	summaryModel := &controlledModel{
		response:   "interactive checkpoint",
		stopReason: llm.StopReasonStop,
	}
	application := &application{dependencies: dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return summaryModel, nil
		},
		compactionKeepRecentTokens: 1,
	}}
	runner := &interactiveSession{
		application: application,
		store:       store,
		history:     history,
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name: "compact",
	})
	if err != nil {
		t.Fatalf("/compact error = %v", err)
	}
	if !strings.Contains(output, "retained 1 recent turn(s)") {
		t.Errorf("/compact output = %q, want retained count", output)
	}
	if len(runner.history) != 3 {
		t.Fatalf("history after compaction = %d, want summary and recent turn", len(runner.history))
	}
	summary, ok := runner.history[0].(llm.CompactionSummaryMessage)
	if !ok || summary.Summary != "interactive checkpoint" {
		t.Fatalf("compaction summary = %#v", runner.history[0])
	}
	assertInteractiveTextMessage(t, runner.history[1], llm.RoleUser, "second prompt")
	assertInteractiveTextMessage(t, runner.history[2], llm.RoleAssistant, "second answer")

	updated, err := store.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(updated.Compactions) != 1 {
		t.Fatalf("compactions = %#v, want one checkpoint", updated.Compactions)
	}
}

func TestInteractiveSessionSlashCommandsRejectInvalidArguments(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{store: new(session.Store)}
	tests := []struct {
		name    string
		request tui.SlashCommandRequest
		want    string
	}{
		{
			name: "tree arguments",
			request: tui.SlashCommandRequest{
				Name:      "tree",
				Arguments: "extra",
			},
			want: "does not accept arguments",
		},
		{
			name:    "missing checkout entry",
			request: tui.SlashCommandRequest{Name: "checkout"},
			want:    "usage: /checkout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := runner.RunSlashCommand(t.Context(), tt.request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunSlashCommand() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInteractiveSessionSlashCommandsPersistRuntimeSettings(t *testing.T) {
	t.Parallel()

	type savedSetting struct {
		setting config.Setting
		value   string
	}
	var saved []savedSetting
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(
				setting config.Setting,
				value string,
			) error {
				saved = append(saved, savedSetting{
					setting: setting,
					value:   value,
				})
				return nil
			},
		}},
		model: deepseek.DefaultModel(),
		configuration: config.Config{
			Provider:       string(deepseek.ProviderID),
			Model:          deepseek.ModelV4Flash,
			DeepSeekAPIKey: "secret",
			Paths: config.Paths{
				GlobalSettings: "/global/settings.json",
				GlobalAuth:     "/global/auth.json",
			},
		},
	}

	settings, err := runner.RunSlashCommand(
		t.Context(),
		tui.SlashCommandRequest{Name: "settings"},
	)
	if err != nil {
		t.Fatalf("/settings error = %v", err)
	}
	for _, want := range []string{
		"Provider: deepseek",
		"Model: " + deepseek.ModelV4Flash,
		"Thinking: default",
		"API key: configured",
		"Global settings: /global/settings.json",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("/settings output = %q, want %q", settings, want)
		}
	}

	output, err := runner.RunSlashCommand(
		t.Context(),
		tui.SlashCommandRequest{
			Name:      "model",
			Arguments: deepseek.ModelV4Pro,
		},
	)
	if err != nil {
		t.Fatalf("/model error = %v", err)
	}
	if !strings.Contains(output, "global settings") {
		t.Errorf("/model output = %q, want global settings", output)
	}
	state := runner.RuntimeState()
	if state.Model.ID != deepseek.ModelV4Pro {
		t.Errorf("runtime model = %q, want V4 Pro", state.Model.ID)
	}

	_, err = runner.RunSlashCommand(
		t.Context(),
		tui.SlashCommandRequest{
			Name:      "thinking",
			Arguments: "off",
		},
	)
	if err != nil {
		t.Fatalf("/thinking error = %v", err)
	}
	state = runner.RuntimeState()
	if state.Thinking != llm.ThinkingLevelOff {
		t.Errorf("runtime thinking = %q, want off", state.Thinking)
	}

	wantSaved := []savedSetting{
		{
			setting: config.SettingModel,
			value:   deepseek.ModelV4Pro,
		},
		{
			setting: config.SettingThinking,
			value:   "off",
		},
	}
	if !reflect.DeepEqual(saved, wantSaved) {
		t.Errorf("saved settings = %#v, want %#v", saved, wantSaved)
	}
}

func TestInteractiveSessionConfigurationCommandsRejectUnsupportedValues(
	t *testing.T,
) {
	t.Parallel()

	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(
				config.Setting,
				string,
			) error {
				t.Fatal("invalid setting was persisted")
				return nil
			},
		}},
		model:         deepseek.DefaultModel(),
		configuration: config.Config{Provider: string(deepseek.ProviderID)},
	}
	tests := []struct {
		name    string
		request tui.SlashCommandRequest
		want    string
	}{
		{
			name: "provider",
			request: tui.SlashCommandRequest{
				Name:      "provider",
				Arguments: "other",
			},
			want: "unsupported provider",
		},
		{
			name: "model",
			request: tui.SlashCommandRequest{
				Name:      "model",
				Arguments: "missing",
			},
			want: "unsupported model",
		},
		{
			name: "thinking",
			request: tui.SlashCommandRequest{
				Name:      "thinking",
				Arguments: "extreme",
			},
			want: "unsupported thinking level",
		},
		{
			name: "extra value",
			request: tui.SlashCommandRequest{
				Name:      "model",
				Arguments: "extra " + deepseek.ModelV4Pro,
			},
			want: "usage: /model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := runner.RunSlashCommand(t.Context(), tt.request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunSlashCommand() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInteractiveSessionLoginCanRetryAfterPersistenceFailure(t *testing.T) {
	t.Parallel()

	saveAttempts := 0
	wantErr := errors.New("credential write interrupted")
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveAPIKey: func(string) (string, error) {
				saveAttempts++
				if saveAttempts == 1 {
					return "", wantErr
				}
				return "/global/auth.json", nil
			},
			newModel: func(config.Config) (agent.Model, error) {
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
		}},
		model: deepseek.DefaultModel(),
		configuration: config.Config{
			Provider: string(deepseek.ProviderID),
			Model:    deepseek.ModelV4Flash,
		},
	}
	request := tui.SlashCommandRequest{
		Name:   "login",
		Secret: "secret-value",
	}

	if _, err := runner.RunSlashCommand(
		t.Context(),
		request,
	); !errors.Is(err, wantErr) {
		t.Fatalf("first /login error = %v, want persistence failure", err)
	}
	if runner.loop != nil || runner.RuntimeState().APIKeyConfigured {
		t.Fatal("failed /login changed runtime authentication state")
	}

	output, err := runner.RunSlashCommand(t.Context(), request)
	if err != nil {
		t.Fatalf("second /login error = %v", err)
	}
	if runner.loop == nil || !runner.RuntimeState().APIKeyConfigured {
		t.Fatal("second /login did not enable current Session")
	}
	if strings.Contains(output, request.Secret) {
		t.Fatalf("/login output exposes API key: %q", output)
	}
}

func openInteractiveCommandStore(
	t *testing.T,
	workspacePath string,
	sessionPath string,
) (*session.Store, session.Snapshot) {
	t.Helper()

	workspace, err := tool.NewWorkspace(workspacePath)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	store, snapshot, err := openExistingSession(
		t.Context(),
		workspace,
		sessionPath,
	)
	if err != nil {
		t.Fatalf("openExistingSession() error = %v", err)
	}
	return store, snapshot
}

func assertInteractiveTextMessage(
	t *testing.T,
	message llm.AgentMessage,
	role llm.Role,
	text string,
) {
	t.Helper()

	standard, ok := message.(llm.Message)
	if !ok {
		t.Fatalf("message = %T, want standard LLM message", message)
	}
	assertTextMessage(t, standard, role, text)
}

func interactiveSlashCommand(
	t *testing.T,
	commands []tui.SlashCommand,
	name string,
) tui.SlashCommand {
	t.Helper()
	for _, command := range commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("slash command /%s not found: %#v", name, commands)
	return tui.SlashCommand{}
}
