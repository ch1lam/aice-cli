package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/skill"
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
	state := runner.RuntimeState()
	if !state.SessionChanged {
		t.Fatal("RuntimeState().SessionChanged = false, want true after /checkout")
	}
	if again := runner.RuntimeState(); again.SessionChanged {
		t.Fatal("RuntimeState().SessionChanged stayed true after the first read")
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
	selectedModel, exists := modelForProvider(
		defaultProviders(),
		string(deepseek.ProviderID),
		deepseek.ModelV4Pro,
	)
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
		providers: defaultProviders(),
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

func TestInteractiveSessionLoginMenuSeparatesSavedCredentialActions(
	t *testing.T,
) {
	t.Parallel()

	runner := &interactiveSession{
		configuration: config.Config{
			Provider:       string(deepseek.ProviderID),
			DeepSeekAPIKey: "saved-deepseek-key",
		},
		providers: defaultProviders(),
	}

	login := interactiveSlashCommand(t, runner.SlashCommands(), "login")
	deepSeek := login.Menu.Options[0]
	if deepSeek.Menu == nil {
		t.Fatal("configured provider did not open the credential action menu")
	}
	if got, want := deepSeek.Menu.Title, "DeepSeek credential"; got != want {
		t.Errorf("credential menu title = %q, want %q", got, want)
	}
	if len(deepSeek.Menu.Options) != 2 {
		t.Fatalf("credential menu options = %d, want 2", len(deepSeek.Menu.Options))
	}
	saved, replace := deepSeek.Menu.Options[0], deepSeek.Menu.Options[1]
	if !saved.UseSavedCredential || saved.Arguments != string(deepseek.ProviderID) {
		t.Errorf("saved credential option = %#v", saved)
	}
	if replace.UseSavedCredential || replace.Arguments != string(deepseek.ProviderID) {
		t.Errorf("replacement credential option = %#v", replace)
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
		providers:                  defaultProviders(),
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
			name: "skills arguments",
			request: tui.SlashCommandRequest{
				Name:      "skills",
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

func TestInteractiveSessionSlashCommandSkills(t *testing.T) {
	t.Parallel()

	workspace := filepath.FromSlash("/workspace")
	userDir := filepath.FromSlash("/home/me/.agents/skills/pdf")
	projectRoot := filepath.Join(workspace, ".agents", "skills")
	projectDir := filepath.Join(projectRoot, "review")
	catalog, _ := skill.Merge(
		[]skill.Skill{{
			Name:        "create-skill",
			Description: "Create Agent Skills",
			Source:      skill.SourceBuiltin,
		}},
		[]skill.Skill{{
			Name:        "pdf",
			Description: "Read PDF files",
			Source:      skill.SourceUser,
			Dir:         userDir,
		}},
		[]skill.Skill{{
			Name:        "review",
			Description: "Review local changes",
			Source:      skill.SourceProject,
			Dir:         projectDir,
		}},
	)
	longDescription := strings.Repeat("a", maxSkillDescriptionRunes+8)
	longCatalog, _ := skill.Merge([]skill.Skill{{
		Name:        "long",
		Description: longDescription,
		Source:      skill.SourceBuiltin,
	}})

	tests := []struct {
		name       string
		session    *interactiveSession
		want       []string
		wantAbsent []string
	}{
		{
			name: "grouped listing",
			session: &interactiveSession{
				skills:        catalog,
				workspacePath: workspace,
			},
			want: []string{
				strings.Join([]string{
					"Skills",
					"",
					"builtin",
					"create-skill — Create Agent Skills (embedded)",
					"",
					"user (~/.agents/skills)",
					"pdf — Read PDF files (" + userDir + ")",
					"",
					"project (" + projectRoot + ")",
					"review — Review local changes (" + projectDir + ")",
					"",
					skillsScanReminder,
				}, "\n"),
			},
		},
		{
			name: "diagnostics",
			session: &interactiveSession{
				skills: catalog,
				skillDiags: []skill.Diagnostic{
					{
						Level:   skill.LevelWarn,
						Dir:     filepath.FromSlash("/home/me/.agents/skills/shared"),
						Message: `skill "shared" from user shadowed by project`,
					},
					{
						Level:   skill.LevelError,
						Message: "skip user skills: home unavailable",
					},
				},
				workspacePath: workspace,
			},
			want: []string{
				"Diagnostics",
				`warn ` + filepath.FromSlash("/home/me/.agents/skills/shared") +
					`: skill "shared" from user shadowed by project`,
				"error skip user skills: home unavailable",
				skillsScanReminder,
			},
		},
		{
			name:    "empty hint",
			session: &interactiveSession{},
			want: []string{
				"No skills found.",
				"Install with: npx skills add <owner/repo>",
				skillsScanReminder,
			},
			wantAbsent: []string{"Diagnostics"},
		},
		{
			name: "diagnostics without skills",
			session: &interactiveSession{
				skillDiags: []skill.Diagnostic{{
					Level:   skill.LevelError,
					Message: "skip builtin skills: broken embed",
				}},
			},
			want: []string{
				"Diagnostics",
				"error skip builtin skills: broken embed",
				skillsScanReminder,
			},
			wantAbsent: []string{"npx skills add", "No skills found."},
		},
		{
			name: "truncates long description",
			session: &interactiveSession{
				skills: longCatalog,
			},
			want: []string{
				"long — " + strings.Repeat("a", maxSkillDescriptionRunes-1) +
					"… (embedded)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			output, err := tt.session.RunSlashCommand(
				t.Context(),
				tui.SlashCommandRequest{Name: "skills"},
			)
			if err != nil {
				t.Fatalf("/skills error = %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(output, want) {
					t.Errorf("/skills output = %q, want %q", output, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(output, absent) {
					t.Errorf("/skills output = %q, does not want %q", output, absent)
				}
			}
		})
	}

	command := interactiveSlashCommand(
		t,
		(&interactiveSession{}).SlashCommands(),
		"skills",
	)
	if command.Menu != nil {
		t.Fatal("/skills unexpectedly has a menu")
	}
	if command.Description == "" {
		t.Fatal("/skills description is empty")
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
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelMedium,
		},
		configuration: config.Config{
			Provider:       string(deepseek.ProviderID),
			Model:          deepseek.ModelV4Flash,
			DeepSeekAPIKey: "secret",
			Paths: config.Paths{
				GlobalSettings: "/global/settings.json",
				GlobalAuth:     "/global/auth.json",
			},
		},
		providers: defaultProviders(),
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
		"Thinking: medium",
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
	if state.Thinking != tui.DisplayThinkingOff {
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
		providers:     defaultProviders(),
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
	var savedSettings []config.Setting
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveAPIKey: func(string, string) (string, error) {
				saveAttempts++
				if saveAttempts == 1 {
					return "", wantErr
				}
				return "/global/auth.json", nil
			},
			saveSetting: func(setting config.Setting, _ string) error {
				savedSettings = append(savedSettings, setting)
				return nil
			},
			newModel: func(config.Config) (agent.Model, error) {
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
			providers: defaultProviders(),
		}},
		model: deepseek.DefaultModel(),
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelMedium,
		},
		configuration: config.Config{
			Provider: string(deepseek.ProviderID),
			Model:    deepseek.ModelV4Flash,
		},
		providers: defaultProviders(),
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
	if len(savedSettings) != 0 {
		t.Fatalf("failed /login persisted settings: %v", savedSettings)
	}

	output, err := runner.RunSlashCommand(t.Context(), request)
	if err != nil {
		t.Fatalf("second /login error = %v", err)
	}
	if runner.loop == nil || !runner.RuntimeState().APIKeyConfigured {
		t.Fatal("second /login did not enable current Session")
	}
	if got, want := savedSettings, []config.Setting{
		config.SettingProvider,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("persisted settings = %v, want %v", got, want)
	}
	if strings.Contains(output, request.Secret) {
		t.Fatalf("/login output exposes API key: %q", output)
	}
}

func TestInteractiveSessionOpencodeMenusAndModelSelection(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(config.Setting, string) error { return nil },
		}},
		model: opencode.DefaultModel(),
		configuration: config.Config{
			Provider: string(opencode.ProviderID),
			Model:    opencode.DefaultModel().ID,
		},
		providers: defaultProviders(),
	}

	commands := runner.SlashCommands()
	modelCommand := interactiveSlashCommand(t, commands, "model")
	if got, want := len(modelCommand.Menu.Options), len(opencode.Models()); got != want {
		t.Fatalf("/model options = %d, want %d", got, want)
	}
	providerCommand := interactiveSlashCommand(t, commands, "provider")
	if len(providerCommand.Menu.Options) != 4 {
		t.Fatalf("/provider options = %d, want 4", len(providerCommand.Menu.Options))
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "model",
		Arguments: "kimi-k2.6",
	})
	if err != nil {
		t.Fatalf("/model error = %v", err)
	}
	if runner.model.ID != "kimi-k2.6" || runner.model.Provider != opencode.ProviderID {
		t.Errorf("/model selected = %#v, want kimi-k2.6 via opencode-go", runner.model)
	}
	if !strings.Contains(output, "kimi-k2.6") {
		t.Errorf("/model output = %q, want kimi-k2.6", output)
	}
}

func TestInteractiveSessionThinkingKeepsRequestedLevelAndClampsPerModel(t *testing.T) {
	t.Parallel()

	type savedSetting struct {
		setting config.Setting
		value   string
	}
	var saved []savedSetting
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(setting config.Setting, value string) error {
				saved = append(saved, savedSetting{setting: setting, value: value})
				return nil
			},
		}},
		model: opencode.DefaultModel(),
		configuration: config.Config{
			Provider: string(opencode.ProviderID),
			Model:    opencode.DefaultModel().ID,
		},
		providers: defaultProviders(),
	}

	if _, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "thinking",
		Arguments: "xhigh",
	}); err != nil {
		t.Fatalf("/thinking error = %v", err)
	}
	if runner.configuration.Thinking != llm.ThinkingLevelXHigh {
		t.Errorf("stored thinking = %q, want the requested xhigh", runner.configuration.Thinking)
	}
	if runner.options.Thinking != llm.ThinkingLevelMax {
		t.Errorf(
			"effective thinking = %q, want max (DeepSeek clamps xhigh up to max)",
			runner.options.Thinking,
		)
	}
	if got, want := saved, []savedSetting{{
		setting: config.SettingThinking,
		value:   "xhigh",
	}}; !reflect.DeepEqual(got, want) {
		t.Errorf("saved settings = %#v, want %#v", got, want)
	}

	// Switching to gpt-5.6-luna must restore the requested xhigh without
	// rewriting the stored setting.
	saved = nil
	if _, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "model",
		Arguments: "gpt-5.6-luna",
	}); err != nil {
		t.Fatalf("/model error = %v", err)
	}
	if runner.options.Thinking != llm.ThinkingLevelXHigh {
		t.Errorf(
			"effective thinking after switch = %q, want xhigh on gpt-5.6-luna",
			runner.options.Thinking,
		)
	}
	if got, want := saved, []savedSetting{{
		setting: config.SettingModel,
		value:   "gpt-5.6-luna",
	}}; !reflect.DeepEqual(got, want) {
		t.Errorf("saved settings after switch = %#v, want only the model", got)
	}

	// A model that always thinks at high or max still clamps the request.
	if _, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "model",
		Arguments: "glm-5.2",
	}); err != nil {
		t.Fatalf("/model error = %v", err)
	}
	if runner.options.Thinking != llm.ThinkingLevelMax {
		t.Errorf(
			"effective thinking on glm-5.2 = %q, want max (xhigh clamps up to max)",
			runner.options.Thinking,
		)
	}
}

func TestInteractiveSessionLoginOpencode(t *testing.T) {
	t.Parallel()

	var savedProvider, savedKey string
	var savedSettings []config.Setting
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveAPIKey: func(provider, apiKey string) (string, error) {
				savedProvider = provider
				savedKey = apiKey
				return "/global/auth.json", nil
			},
			saveSetting: func(setting config.Setting, value string) error {
				savedSettings = append(savedSettings, setting)
				_ = value
				return nil
			},
			newModel: func(config.Config) (agent.Model, error) {
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
			providers: defaultProviders(),
		}},
		model: opencode.DefaultModel(),
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelMedium,
		},
		configuration: config.Config{
			Provider: string(opencode.ProviderID),
			Model:    opencode.DefaultModel().ID,
		},
		providers: defaultProviders(),
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:   "login",
		Secret: "opencode-secret",
	})
	if err != nil {
		t.Fatalf("/login error = %v", err)
	}
	if savedProvider != string(opencode.ProviderID) || savedKey != "opencode-secret" {
		t.Errorf(
			"saved = %q/%q, want opencode-go/opencode-secret",
			savedProvider,
			savedKey,
		)
	}
	if runner.configuration.OpenCodeAPIKey != "opencode-secret" {
		t.Errorf(
			"configuration.OpenCodeAPIKey = %q, want opencode-secret",
			runner.configuration.OpenCodeAPIKey,
		)
	}
	if runner.model.Provider != opencode.ProviderID {
		t.Errorf(
			"runner.model.Provider = %q, want opencode-go after login",
			runner.model.Provider,
		)
	}
	if !runner.RuntimeState().APIKeyConfigured {
		t.Error("RuntimeState().APIKeyConfigured = false, want true")
	}
	if got, want := savedSettings, []config.Setting{
		config.SettingProvider,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf(
			"persisted settings = %v, want provider only (stored model stays valid)",
			got,
		)
	}
	if !strings.Contains(output, "OpenCode Go API key") {
		t.Errorf("/login output = %q, want OpenCode Go mention", output)
	}
}

func TestInteractiveSessionLoginUsesSavedCredentialWithoutSaving(t *testing.T) {
	t.Parallel()

	var savedSettings []config.Setting
	var modelConfiguration config.Config
	saveAttempts := 0
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveAPIKey: func(string, string) (string, error) {
				saveAttempts++
				return "/global/auth.json", nil
			},
			saveSetting: func(setting config.Setting, _ string) error {
				savedSettings = append(savedSettings, setting)
				return nil
			},
			newModel: func(configuration config.Config) (agent.Model, error) {
				modelConfiguration = configuration
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
			providers: defaultProviders(),
		}},
		model: opencode.DefaultModel(),
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelMedium,
		},
		configuration: config.Config{
			Provider:       string(deepseek.ProviderID),
			Model:          "gpt-5.6-terra",
			DeepSeekAPIKey: "deepseek-key",
			OpenCodeAPIKey: "opencode-key",
		},
		providers: defaultProviders(),
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:               "login",
		Arguments:          string(opencode.ProviderID),
		UseSavedCredential: true,
	})
	if err != nil {
		t.Fatalf("/login with saved credential error = %v", err)
	}
	if saveAttempts != 0 {
		t.Fatalf("save API key attempts = %d, want 0", saveAttempts)
	}
	if modelConfiguration.OpenCodeAPIKey != "opencode-key" {
		t.Errorf(
			"model configuration OpenCodeAPIKey = %q, want saved key",
			modelConfiguration.OpenCodeAPIKey,
		)
	}
	if runner.configuration.Provider != string(opencode.ProviderID) {
		t.Errorf(
			"configuration.Provider = %q, want opencode-go",
			runner.configuration.Provider,
		)
	}
	if runner.model.Provider != opencode.ProviderID {
		t.Errorf("runner.model.Provider = %q, want opencode-go", runner.model.Provider)
	}
	if got, want := savedSettings, []config.Setting{
		config.SettingProvider,
		config.SettingModel,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("persisted settings = %v, want %v", got, want)
	}
	if !strings.Contains(output, "saved credential") {
		t.Errorf("/login output = %q, want saved credential message", output)
	}
}

func TestInteractiveSessionLoginPersistsProviderAndFallsBackModel(t *testing.T) {
	t.Parallel()

	var savedSettings []config.Setting
	var savedValues []string
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveAPIKey: func(string, string) (string, error) {
				return "/global/auth.json", nil
			},
			saveSetting: func(setting config.Setting, value string) error {
				savedSettings = append(savedSettings, setting)
				savedValues = append(savedValues, value)
				return nil
			},
			newModel: func(configuration config.Config) (agent.Model, error) {
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
			providers: defaultProviders(),
		}},
		// The stored model is OpenAI-only, so logging into
		// OpenCode Go must persist both the provider and its default model;
		// otherwise a restart would fail with an unsupported-model error.
		model: opencode.DefaultModel(),
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelMedium,
		},
		configuration: config.Config{
			Provider:     string(opencode.ProviderID),
			Model:        "gpt-5.6-terra",
			OpenAIAPIKey: "openai-key",
		},
		providers: defaultProviders(),
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:   "login",
		Secret: "opencode-secret",
	})
	if err != nil {
		t.Fatalf("/login error = %v", err)
	}
	if runner.configuration.Provider != string(opencode.ProviderID) {
		t.Errorf(
			"configuration.Provider = %q, want opencode-go",
			runner.configuration.Provider,
		)
	}
	if runner.configuration.Model != opencode.DefaultModel().ID {
		t.Errorf(
			"configuration.Model = %q, want the opencode-go default %q",
			runner.configuration.Model,
			opencode.DefaultModel().ID,
		)
	}
	if runner.model.Provider != opencode.ProviderID {
		t.Errorf(
			"runner.model.Provider = %q, want opencode-go after login",
			runner.model.Provider,
		)
	}
	if got, want := savedSettings, []config.Setting{
		config.SettingProvider,
		config.SettingModel,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("persisted settings = %v, want provider then model", got)
	}
	if len(savedValues) != 2 ||
		savedValues[0] != string(opencode.ProviderID) ||
		savedValues[1] != opencode.DefaultModel().ID {
		t.Errorf(
			"persisted values = %v, want [%s %s]",
			savedValues,
			opencode.ProviderID,
			opencode.DefaultModel().ID,
		)
	}
	_ = output
}

func TestInteractiveSessionProviderSwitchPersistsFallenBackModel(t *testing.T) {
	t.Parallel()

	var savedSettings []config.Setting
	var savedValues []string
	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(setting config.Setting, value string) error {
				savedSettings = append(savedSettings, setting)
				savedValues = append(savedValues, value)
				return nil
			},
			newModel: func(configuration config.Config) (agent.Model, error) {
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
			providers: defaultProviders(),
		}},
		// The stored model is OpenAI-only, so switching the
		// provider to OpenCode Go must persist its default model too.
		model: opencode.DefaultModel(),
		options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelMedium,
		},
		configuration: config.Config{
			Provider:       string(opencode.ProviderID),
			Model:          "gpt-5.6-terra",
			OpenCodeAPIKey: "opencode-key",
			OpenAIAPIKey:   "openai-key",
		},
		providers: defaultProviders(),
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "provider",
		Arguments: string(opencode.ProviderID),
	})
	if err != nil {
		t.Fatalf("/provider error = %v", err)
	}
	if got, want := savedSettings, []config.Setting{
		config.SettingProvider,
		config.SettingModel,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("persisted settings = %v, want provider then model", got)
	}
	if len(savedValues) != 2 ||
		savedValues[0] != string(opencode.ProviderID) ||
		savedValues[1] != opencode.DefaultModel().ID {
		t.Errorf(
			"persisted values = %v, want [%s %s]",
			savedValues,
			opencode.ProviderID,
			opencode.DefaultModel().ID,
		)
	}
	if runner.configuration.Model != opencode.DefaultModel().ID {
		t.Errorf(
			"configuration.Model = %q, want the opencode-go default %q",
			runner.configuration.Model,
			opencode.DefaultModel().ID,
		)
	}
	if !strings.Contains(output, "opencode-go") {
		t.Errorf("/provider output = %q, want opencode-go", output)
	}
}

func TestInteractiveSessionProviderSwitchRebuildsLoopAndModel(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{
		application: &application{dependencies: dependencies{
			saveSetting: func(config.Setting, string) error { return nil },
			newModel: func(configuration config.Config) (agent.Model, error) {
				if configuration.OpenCodeAPIKey == "" {
					t.Error("newModel called without opencode credential")
				}
				return &controlledModel{
					response:   "ready",
					stopReason: llm.StopReasonStop,
				}, nil
			},
			providers: defaultProviders(),
		}},
		model: deepseek.DefaultModel(),
		configuration: config.Config{
			Provider:       string(deepseek.ProviderID),
			Model:          deepseek.ModelV4Flash,
			OpenCodeAPIKey: "opencode-key",
		},
		providers: defaultProviders(),
	}

	output, err := runner.RunSlashCommand(t.Context(), tui.SlashCommandRequest{
		Name:      "provider",
		Arguments: string(opencode.ProviderID),
	})
	if err != nil {
		t.Fatalf("/provider error = %v", err)
	}
	if runner.configuration.Provider != string(opencode.ProviderID) {
		t.Errorf(
			"configuration.Provider = %q, want opencode-go",
			runner.configuration.Provider,
		)
	}
	if runner.model.Provider != opencode.ProviderID {
		t.Errorf("model.Provider = %q, want opencode-go", runner.model.Provider)
	}
	if runner.model.ID != "deepseek-v4-flash" {
		t.Errorf(
			"model.ID = %q, want the opencode default deepseek-v4-flash",
			runner.model.ID,
		)
	}
	if runner.loop == nil {
		t.Error("/provider did not rebuild the agent loop")
	}
	if !strings.Contains(output, "opencode-go") {
		t.Errorf("/provider output = %q, want opencode-go", output)
	}
}

func TestInteractiveSessionLoopRebuildPreservesGuardContext(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name    string
		request tui.SlashCommandRequest
	}{
		{
			name: "provider",
			request: tui.SlashCommandRequest{
				Name:      "provider",
				Arguments: string(opencode.ProviderID),
			},
		},
		{
			name: "login",
			request: tui.SlashCommandRequest{
				Name:               "login",
				Arguments:          string(opencode.ProviderID),
				UseSavedCredential: true,
			},
		},
	}
	accesses := []struct {
		name    string
		outside bool
		granted bool
	}{
		{name: "workspace path"},
		{
			name:    "outside path",
			outside: true,
		},
		{
			name:    "session path grant",
			outside: true,
			granted: true,
		},
	}

	for _, command := range commands {
		for _, access := range accesses {
			t.Run(command.name+" "+access.name, func(t *testing.T) {
				workspacePath := t.TempDir()
				readPath := "README.md"
				if access.outside {
					readPath = filepath.Join(t.TempDir(), "outside.txt")
				}
				arguments, err := json.Marshal(map[string]string{"path": readPath})
				if err != nil {
					t.Fatalf("json.Marshal() error = %v", err)
				}
				call := llm.ToolCall{
					ID:        "read-1",
					Name:      "read",
					Arguments: arguments,
				}
				model := &toolLoopModel{firstCall: &call}
				executions := 0
				readTool := newAppTestTool(
					"read",
					func(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
						executions++
						return llm.ToolResult{
							CallID:  call.ID,
							Name:    call.Name,
							Content: []llm.ContentPart{llm.NewTextContent("read").Part()},
						}, nil
					},
				)
				gate, err := guard.New(workspacePath, guard.Config{})
				if err != nil {
					t.Fatalf("guard.New() error = %v", err)
				}
				if access.granted {
					gate.AllowPathSession(readPath, false)
				}
				adapter := &guardAdapter{inner: gate}
				application := &application{dependencies: dependencies{
					saveSetting: func(config.Setting, string) error { return nil },
					newModel: func(config.Config) (agent.Model, error) {
						return model, nil
					},
					providers: defaultProviders(),
				}}
				runner := &interactiveSession{
					application: application,
					model:       deepseek.DefaultModel(),
					configuration: config.Config{
						Provider:       string(deepseek.ProviderID),
						Model:          deepseek.ModelV4Flash,
						DeepSeekAPIKey: "deepseek-key",
						OpenCodeAPIKey: "opencode-key",
					},
					tools:         []agent.Tool{readTool},
					providers:     defaultProviders(),
					guard:         gate,
					guardAdapter:  adapter,
					guardRequests: make(chan interaction.GuardRequest, 1),
				}

				if _, err := runner.RunSlashCommand(t.Context(), command.request); err != nil {
					t.Fatalf("/%s error = %v", command.request.Name, err)
				}
				prompt, err := llm.NewUserMessage(
					llm.NewTextContent("inspect").Part(),
				)
				if err != nil {
					t.Fatalf("llm.NewUserMessage() error = %v", err)
				}
				type runOutcome struct {
					result agent.Result
					err    error
				}
				runCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				done := make(chan runOutcome, 1)
				go func() {
					result, err := runner.loop.Run(runCtx, agent.RunInput{
						Model:  runner.model,
						Prompt: prompt,
					}, nil)
					done <- runOutcome{result: result, err: err}
				}()

				var outcome runOutcome
				if access.outside && !access.granted {
					select {
					case request := <-runner.guardRequests:
						if request.Path != readPath {
							t.Errorf("guard request path = %q, want %q", request.Path, readPath)
						}
						request.Reply <- interaction.GuardReply{OptionID: guardOptionAllowOnce}
					case outcome = <-done:
						t.Fatalf("rebuilt loop finished without guard confirmation: %v", outcome.err)
					}
				}
				if !access.outside || access.granted {
					select {
					case request := <-runner.guardRequests:
						request.Reply <- interaction.GuardReply{OptionID: guardOptionDeny}
						t.Fatalf("workspace read unexpectedly requested confirmation: %s", request.Reason)
					case outcome = <-done:
					}
				} else {
					outcome = <-done
				}
				if outcome.err != nil {
					t.Fatalf("rebuilt loop Run() error = %v", outcome.err)
				}
				if executions != 1 {
					t.Fatalf("read executions = %d, want 1", executions)
				}
				if len(outcome.result.ModelRounds) == 0 ||
					len(outcome.result.ModelRounds[0].ToolResults) != 1 ||
					outcome.result.ModelRounds[0].ToolResults[0].IsError {
					t.Fatalf("rebuilt loop tool result = %#v, want success", outcome.result.ModelRounds)
				}
			})
		}
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
