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
		loadConfig: func(string) (config.Config, error) {
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
		workspace string
		scope     config.Scope
		setting   config.Setting
		value     string
	}
	var saved []savedSetting
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(
				workspace string,
				scope config.Scope,
				setting config.Setting,
				value string,
			) error {
				saved = append(saved, savedSetting{
					workspace: workspace,
					scope:     scope,
					setting:   setting,
					value:     value,
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
				GlobalSettings:  "/global/settings.json",
				ProjectSettings: "/workspace/.aice/settings.json",
				GlobalAuth:      "/global/auth.json",
			},
		},
		workspace: "/workspace",
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
		"/workspace/.aice/settings.json",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("/settings output = %q, want %q", settings, want)
		}
	}

	output, err := runner.RunSlashCommand(
		t.Context(),
		tui.SlashCommandRequest{
			Name:      "model",
			Arguments: "--local " + deepseek.ModelV4Pro,
		},
	)
	if err != nil {
		t.Fatalf("/model error = %v", err)
	}
	if !strings.Contains(output, "project settings") {
		t.Errorf("/model output = %q, want project scope", output)
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
			workspace: "/workspace",
			scope:     config.ScopeProject,
			setting:   config.SettingModel,
			value:     deepseek.ModelV4Pro,
		},
		{
			workspace: "/workspace",
			scope:     config.ScopeGlobal,
			setting:   config.SettingThinking,
			value:     "off",
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
				string,
				config.Scope,
				config.Setting,
				string,
			) error {
				t.Fatal("invalid setting was persisted")
				return nil
			},
		}},
		model:         deepseek.DefaultModel(),
		configuration: config.Config{Provider: string(deepseek.ProviderID)},
		workspace:     "/workspace",
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
			name: "scope",
			request: tui.SlashCommandRequest{
				Name:      "model",
				Arguments: "--global " + deepseek.ModelV4Pro,
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
			saveAPIKey: func(string, string) (string, error) {
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
		workspace: "/workspace",
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
