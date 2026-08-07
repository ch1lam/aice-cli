package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestApplicationPrintRunsBuiltInAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name:     "adds final newline",
			response: "inspection complete",
			want:     "inspection complete\n",
		},
		{
			name:     "preserves existing newline",
			response: "inspection complete\n",
			want:     "inspection complete\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workspace := t.TempDir()
			model := &recordingModel{response: tt.response}
			wantConfig := config.Config{
				Provider:        string(deepseek.ProviderID),
				Model:           deepseek.ModelV4Flash,
				DeepSeekAPIKey:  "test-key",
				DeepSeekBaseURL: "https://deepseek.example/anthropic",
			}
			command, err := newCommand(dependencies{
				loadConfig: func() (config.Config, error) {
					return wantConfig, nil
				},
				newModel: func(got config.Config) (agent.Model, error) {
					if got != wantConfig {
						t.Errorf("model config = %#v, want %#v", got, wantConfig)
					}
					return model, nil
				},
			})
			if err != nil {
				t.Fatalf("newCommand() error = %v", err)
			}

			output := new(bytes.Buffer)
			command.SetOut(output)
			command.SetArgs([]string{
				"--workspace", workspace,
				"--print", "inspect this repository",
			})
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("ExecuteContext() error = %v", err)
			}
			if got := output.String(); got != tt.want {
				t.Errorf("command output = %q, want %q", got, tt.want)
			}

			if len(model.requests) != 1 {
				t.Fatalf("model requests = %d, want 1", len(model.requests))
			}
			request := model.requests[0]
			if request.SystemPrompt != defaultSystemPrompt {
				t.Errorf("system prompt = %q, want %q", request.SystemPrompt, defaultSystemPrompt)
			}
			if len(request.Messages) != 1 {
				t.Fatalf("model messages = %#v, want one user prompt", request.Messages)
			}
			user, ok := request.Messages[0].(llm.UserMessage)
			if !ok ||
				len(user.Content) != 1 ||
				user.Content[0].Text != "inspect this repository" {
				t.Errorf("model messages = %#v, want one user prompt", request.Messages)
			}

			toolNames := make([]string, len(request.Tools))
			for index, definition := range request.Tools {
				toolNames[index] = definition.Name
			}
			want := []string{"read", "write", "edit", "bash", "grep", "find", "ls"}
			if !reflect.DeepEqual(toolNames, want) {
				t.Errorf("model tools = %v, want %v", toolNames, want)
			}
		})
	}
}

func TestApplicationPrintUsesConfiguredModelAndThinking(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	model := &recordingModel{response: "configured"}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Provider:       string(deepseek.ProviderID),
				Model:          deepseek.ModelV4Pro,
				Thinking:       llm.ThinkingLevelHigh,
				DeepSeekAPIKey: "test-key",
			}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{
		"--workspace", workspace,
		"--print", "inspect",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want one", len(model.requests))
	}
	request := model.requests[0]
	if request.Model.ID != deepseek.ModelV4Pro {
		t.Errorf(
			"request model = %q, want %q",
			request.Model.ID,
			deepseek.ModelV4Pro,
		)
	}
	if request.Options.Thinking != llm.ThinkingLevelHigh {
		t.Errorf(
			"request thinking = %q, want high",
			request.Options.Thinking,
		)
	}
}

func TestResolveModelSettingsRejectsUnsupportedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config config.Config
		want   string
	}{
		{
			name:   "provider",
			config: config.Config{Provider: "other"},
			want:   "unsupported provider",
		},
		{
			name:   "model",
			config: config.Config{Model: "missing"},
			want:   "unsupported model",
		},
		{
			name: "thinking",
			config: config.Config{
				Thinking: llm.ThinkingLevel("extreme"),
			},
			want: "unsupported thinking level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := resolveModelSettings(defaultProviders(), tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("resolveModelSettings() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveModelSettingsOpencode(t *testing.T) {
	t.Parallel()

	model, options, err := resolveModelSettings(defaultProviders(), config.Config{
		Provider: string(opencode.ProviderID),
		Model:    "kimi-k2.6",
		Thinking: llm.ThinkingLevelMedium,
	})
	if err != nil {
		t.Fatalf("resolveModelSettings() error = %v", err)
	}
	if model.Provider != opencode.ProviderID || model.ID != "kimi-k2.6" {
		t.Errorf("model = %#v, want kimi-k2.6 via opencode-go", model)
	}
	if options.Thinking != llm.ThinkingLevelMedium {
		t.Errorf("options.Thinking = %q, want medium", options.Thinking)
	}
}

func TestResolveModelSettingsOpencodeDefaultModel(t *testing.T) {
	t.Parallel()

	model, _, err := resolveModelSettings(defaultProviders(), config.Config{
		Provider: string(opencode.ProviderID),
	})
	if err != nil {
		t.Fatalf("resolveModelSettings() error = %v", err)
	}
	if model.ID != "deepseek-v4-flash" {
		t.Errorf("default model = %q, want deepseek-v4-flash", model.ID)
	}
}

func TestApplicationPrintReturnsConfigurationError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("configuration unavailable")
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, wantErr
		},
		newModel: func(config.Config) (agent.Model, error) {
			t.Fatal("model factory called after configuration failure")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetArgs([]string{"--print", "inspect"})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}
}

func TestApplicationPrintSeparatesToolLoopTurns(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	model := &toolLoopModel{}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}

	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetArgs([]string{"--workspace", workspace, "--print", "inspect"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if got, want := output.String(), "checking\ncomplete\n"; got != want {
		t.Errorf("command output = %q, want %q", got, want)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(model.requests))
	}
	secondRequest := model.requests[1]
	if len(secondRequest.Messages) != 3 {
		t.Fatalf("second request messages = %d, want user, assistant, and tool result", len(secondRequest.Messages))
	}
	toolResult, ok := secondRequest.Messages[2].(llm.ToolResultMessage)
	if !ok || toolResult.Role != llm.RoleToolResult {
		t.Errorf("second request last message = %#v, want tool result", secondRequest.Messages[2])
	}
}

func TestApplicationInteractiveKeepsConversationHistory(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	model := &recordingModel{response: "inspection complete"}
	input := strings.NewReader("terminal input")
	output := new(bytes.Buffer)
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
		runTUI: func(ctx context.Context, runner tui.Runner, options tui.Options) error {
			if options.Input != input {
				t.Error("TUI input does not match command input")
			}
			if options.Output != output {
				t.Error("TUI output does not match command output")
			}
			if options.Model.ID != deepseek.ModelV4Flash {
				t.Errorf(
					"TUI model = %q, want %q",
					options.Model.ID,
					deepseek.ModelV4Flash,
				)
			}
			if options.Thinking != tui.DisplayThinkingDefault {
				t.Errorf(
					"TUI thinking = %q, want provider default",
					options.Thinking,
				)
			}
			if options.Usage != (tui.DisplayUsage{}) {
				t.Errorf("TUI usage = %#v, want empty new session usage", options.Usage)
			}
			if options.WorkingDirectory != workspace {
				t.Errorf(
					"TUI working directory = %q, want %q",
					options.WorkingDirectory,
					workspace,
				)
			}
			if err := runner.Run(ctx, "first prompt", nil); err != nil {
				return err
			}
			return runner.Run(ctx, "second prompt", nil)
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetIn(input)
	command.SetOut(output)
	command.SetArgs([]string{"--workspace", workspace})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(model.requests))
	}
	secondRequest := model.requests[1]
	if len(secondRequest.Messages) != 3 {
		t.Fatalf("second request messages = %d, want prior user, prior assistant, and new user", len(secondRequest.Messages))
	}
	firstPrompt, ok := secondRequest.Messages[0].(llm.UserMessage)
	if !ok || len(firstPrompt.Content) != 1 || firstPrompt.Content[0].Text != "first prompt" {
		got := ""
		if ok && len(firstPrompt.Content) > 0 {
			got = firstPrompt.Content[0].Text
		}
		t.Errorf("first history message = %q, want first prompt", got)
	}
	secondPrompt, ok := secondRequest.Messages[2].(llm.UserMessage)
	if !ok || len(secondPrompt.Content) != 1 || secondPrompt.Content[0].Text != "second prompt" {
		got := ""
		if ok && len(secondPrompt.Content) > 0 {
			got = secondPrompt.Content[0].Text
		}
		t.Errorf("current prompt = %q, want second prompt", got)
	}

	paths, err := filepath.Glob(filepath.Join(
		workspace,
		".aice",
		"sessions",
		"*.jsonl",
	))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("session files = %v, want one JSONL file", paths)
	}
	snapshot := openSessionSnapshot(t, paths[0])
	if len(snapshot.Turns) != 2 {
		t.Fatalf("persisted turns = %d, want 2", len(snapshot.Turns))
	}
}

func TestApplicationInteractiveStartsWithoutCredentials(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			t.Fatal("model factory called before credentials were configured")
			return nil, nil
		},
		runTUI: func(
			ctx context.Context,
			runner tui.Runner,
			options tui.Options,
		) error {
			if options.APIKeyConfigured {
				t.Error("TUI reports an API key before login")
			}
			err := runner.Run(ctx, "inspect", nil)
			if err == nil || !strings.Contains(err.Error(), "/login") {
				t.Fatalf("Run() error = %v, want /login guidance", err)
			}
			commandRunner, ok := runner.(tui.SlashCommandRunner)
			if !ok {
				t.Fatal("interactive runner does not expose slash commands")
			}
			settings, err := commandRunner.RunSlashCommand(
				ctx,
				tui.SlashCommandRequest{Name: "settings"},
			)
			if err != nil {
				return err
			}
			if !strings.Contains(settings, "API key: not configured") {
				t.Errorf(
					"/settings output = %q, want unconfigured state",
					settings,
				)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace})

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func TestApplicationInteractiveLoginEnablesCurrentSession(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	model := &recordingModel{response: "ready"}
	var savedAPIKey string
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Paths: config.Paths{
					GlobalSettings: "/global/settings.json",
					GlobalAuth:     "/global/auth.json",
				},
			}, nil
		},
		saveAPIKey: func(_ string, apiKey string) (string, error) {
			savedAPIKey = apiKey
			return "/global/auth.json", nil
		},
		newModel: func(configuration config.Config) (agent.Model, error) {
			if configuration.DeepSeekAPIKey != "secret-value" {
				t.Errorf(
					"model API key = %q, want configured value",
					configuration.DeepSeekAPIKey,
				)
			}
			return model, nil
		},
		runTUI: func(
			ctx context.Context,
			runner tui.Runner,
			options tui.Options,
		) error {
			if options.APIKeyConfigured {
				t.Error("TUI starts authenticated before /login")
			}
			commandRunner := runner.(tui.SlashCommandRunner)
			output, err := commandRunner.RunSlashCommand(
				ctx,
				tui.SlashCommandRequest{
					Name:   "login",
					Secret: "secret-value",
				},
			)
			if err != nil {
				return err
			}
			if strings.Contains(output, "secret-value") {
				t.Fatalf("/login output exposes API key: %q", output)
			}
			state := runner.(tui.RuntimeStateProvider).RuntimeState()
			if !state.APIKeyConfigured {
				t.Error("runtime remains unauthenticated after /login")
			}
			return runner.Run(ctx, "inspect", nil)
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", workspace})

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if savedAPIKey != "secret-value" {
		t.Errorf("saved API key = %q, want configured value", savedAPIKey)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want one after login", len(model.requests))
	}
}

func TestApplicationPrintStillRequiresCredentials(t *testing.T) {
	t.Parallel()

	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			t.Fatal("model factory called without credentials")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--print", "inspect"})

	err = command.ExecuteContext(t.Context())
	if err == nil ||
		!strings.Contains(err.Error(), "API key is not configured") {
		t.Fatalf("ExecuteContext() error = %v, want credential guidance", err)
	}
}

func TestApplicationPrintResumesExplicitSession(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")

	firstModel := &recordingModel{response: "first answer"}
	firstCommand, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return firstModel, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() first error = %v", err)
	}
	firstCommand.SetOut(io.Discard)
	firstCommand.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "first prompt",
	})
	if err := firstCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("first ExecuteContext() error = %v", err)
	}

	secondModel := &recordingModel{response: "second answer"}
	secondCommand, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return secondModel, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() second error = %v", err)
	}
	secondCommand.SetOut(io.Discard)
	secondCommand.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "second prompt",
	})
	if err := secondCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("second ExecuteContext() error = %v", err)
	}

	if len(secondModel.requests) != 1 {
		t.Fatalf("second model requests = %d, want 1", len(secondModel.requests))
	}
	messages := secondModel.requests[0].Messages
	if len(messages) != 3 {
		t.Fatalf(
			"second request messages = %d, want prior user, assistant, and current user",
			len(messages),
		)
	}
	assertTextMessage(t, messages[0], llm.RoleUser, "first prompt")
	assertTextMessage(t, messages[1], llm.RoleAssistant, "first answer")
	assertTextMessage(t, messages[2], llm.RoleUser, "second prompt")

	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 2 {
		t.Fatalf("persisted turns = %d, want 2", len(snapshot.Turns))
	}
}

func TestApplicationInteractiveResumesExplicitSession(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	firstUsage := llm.Usage{
		InputTokens:     120,
		OutputTokens:    30,
		CacheReadTokens: 40,
		TotalTokens:     190,
		Cost: &llm.Cost{
			Input:     0.001,
			Output:    0.002,
			CacheRead: 0.0001,
			Total:     0.0031,
		},
	}
	firstModel := &recordingModel{
		response: "first answer",
		usage:    firstUsage,
	}
	firstCommand, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return firstModel, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() first error = %v", err)
	}
	firstCommand.SetOut(io.Discard)
	firstCommand.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "first prompt",
	})
	if err := firstCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("first ExecuteContext() error = %v", err)
	}

	secondModel := &recordingModel{response: "second answer"}
	secondCommand, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return secondModel, nil
		},
		runTUI: func(
			ctx context.Context,
			runner tui.Runner,
			options tui.Options,
		) error {
			if !reflect.DeepEqual(options.Usage, newDisplayUsage(firstUsage)) {
				t.Errorf(
					"TUI restored usage = %#v, want %#v",
					options.Usage,
					newDisplayUsage(firstUsage),
				)
			}
			return runner.Run(ctx, "second prompt", nil)
		},
	})
	if err != nil {
		t.Fatalf("newCommand() second error = %v", err)
	}
	secondCommand.SetIn(strings.NewReader(""))
	secondCommand.SetOut(io.Discard)
	secondCommand.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
	})
	if err := secondCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("second ExecuteContext() error = %v", err)
	}

	if len(secondModel.requests) != 1 {
		t.Fatalf("second model requests = %d, want 1", len(secondModel.requests))
	}
	messages := secondModel.requests[0].Messages
	if len(messages) != 3 {
		t.Fatalf(
			"interactive request messages = %d, want restored turn and current user",
			len(messages),
		)
	}
	assertTextMessage(t, messages[0], llm.RoleUser, "first prompt")
	assertTextMessage(t, messages[1], llm.RoleAssistant, "first answer")
	assertTextMessage(t, messages[2], llm.RoleUser, "second prompt")
}

func TestApplicationPersistsFailedRunAfterToolSideEffect(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	wantErr := errors.New("provider disconnected")
	call := llm.ToolCall{
		ID:        "write-1",
		Name:      "write",
		Arguments: []byte(`{"path":"changed.txt","content":"changed\n"}`),
	}
	model := &toolLoopModel{firstCall: &call, secondErr: wantErr}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return model, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "inspect",
	})

	err = command.ExecuteContext(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, wantErr)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "changed.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(data), "changed\n"; got != want {
		t.Fatalf("changed.txt = %q, want %q", got, want)
	}

	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 1 {
		t.Fatalf("persisted turns = %#v, want one terminal run", snapshot.Turns)
	}
	messages := snapshot.Turns[0].Messages
	if got, want := persistedMessageRoles(messages), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleAssistant,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted message roles = %v, want %v", got, want)
	}
	result, ok := messages[2].(llm.ToolResultMessage)
	if !ok || result.IsError {
		t.Fatalf("persisted tool result = %#v, want successful write", messages[2])
	}
	terminal, ok := messages[3].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("terminal message = %T, want AssistantMessage", messages[3])
	}
	assertPersistedTerminalAssistant(t, terminal, llm.StopReasonError)
	if strings.Contains(terminal.ErrorMessage, wantErr.Error()) {
		t.Fatalf("terminal error leaked provider detail: %q", terminal.ErrorMessage)
	}

	resumeModel := &recordingModel{response: "recovered"}
	resumeCommand, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key"}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return resumeModel, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() resume error = %v", err)
	}
	resumeCommand.SetOut(io.Discard)
	resumeCommand.SetArgs([]string{
		"--workspace", workspace,
		"--session", sessionPath,
		"--print", "continue",
	})
	if err := resumeCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("resume ExecuteContext() error = %v", err)
	}
	if len(resumeModel.requests) != 1 {
		t.Fatalf("resume model requests = %d, want 1", len(resumeModel.requests))
	}
	if got, want := persistedMessageRoles(resumeModel.requests[0].Messages), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleAssistant,
		llm.RoleUser,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed request roles = %v, want %v", got, want)
	}
}

func TestInteractiveSessionPersistsCancellationAfterToolSideEffect(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	ctx, cancel := context.WithCancel(t.Context())
	tool := newAppTestTool(
		"mutate",
		func(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
			if err := os.WriteFile(
				filepath.Join(workspace, "changed.txt"),
				[]byte("changed\n"),
				0o600,
			); err != nil {
				return llm.ToolResult{}, err
			}
			cancel()
			return llm.ToolResult{
				CallID:  call.ID,
				Name:    call.Name,
				Content: []llm.ContentPart{llm.NewTextContent("changed").Part()},
			}, nil
		},
	)
	call := llm.ToolCall{
		ID:        "mutate-1",
		Name:      "mutate",
		Arguments: json.RawMessage(`{}`),
	}
	model := &toolLoopModel{firstCall: &call}
	loop, err := agent.NewLoop(
		model,
		[]agent.Tool{tool},
	)
	if err != nil {
		t.Fatalf("agent.NewLoop() error = %v", err)
	}
	store := createAppTestSession(t, sessionPath, workspace)
	runner := &interactiveSession{
		loop:  loop,
		store: store,
		model: deepseek.DefaultModel(),
	}

	err = runner.Run(ctx, "mutate then continue", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "changed.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(data), "changed\n"; got != want {
		t.Fatalf("changed.txt = %q, want %q", got, want)
	}
	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 1 {
		t.Fatalf("persisted turns = %#v, want one canceled run", snapshot.Turns)
	}
	messages := snapshot.Turns[0].Messages
	if got, want := persistedMessageRoles(messages), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleAssistant,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted message roles = %v, want %v", got, want)
	}
	terminal, ok := messages[3].(llm.AssistantMessage)
	if !ok {
		t.Fatalf("terminal message = %T, want AssistantMessage", messages[3])
	}
	assertPersistedTerminalAssistant(t, terminal, llm.StopReasonAborted)
}

func TestInteractiveSessionPersistsToolErrorAndRecovery(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	toolFailure := errors.New("disk unavailable")
	tool := newAppTestTool(
		"mutate",
		func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
			return llm.ToolResult{}, toolFailure
		},
	)
	call := llm.ToolCall{
		ID:        "mutate-1",
		Name:      "mutate",
		Arguments: json.RawMessage(`{}`),
	}
	model := &toolLoopModel{firstCall: &call}
	loop, err := agent.NewLoop(
		model,
		[]agent.Tool{tool},
	)
	if err != nil {
		t.Fatalf("agent.NewLoop() error = %v", err)
	}
	store := createAppTestSession(t, sessionPath, workspace)
	runner := &interactiveSession{
		loop:  loop,
		store: store,
		model: deepseek.DefaultModel(),
	}

	if err := runner.Run(t.Context(), "mutate", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 1 {
		t.Fatalf("persisted turns = %#v, want one recovered run", snapshot.Turns)
	}
	messages := snapshot.Turns[0].Messages
	if got, want := persistedMessageRoles(messages), []llm.Role{
		llm.RoleUser,
		llm.RoleAssistant,
		llm.RoleToolResult,
		llm.RoleAssistant,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted message roles = %v, want %v", got, want)
	}
	toolResult, ok := messages[2].(llm.ToolResultMessage)
	if !ok || !toolResult.IsError ||
		len(toolResult.Content) != 1 ||
		!strings.Contains(toolResult.Content[0].Text, toolFailure.Error()) {
		t.Fatalf("persisted tool error = %#v", messages[2])
	}
	final, ok := messages[3].(llm.AssistantMessage)
	if !ok || final.StopReason != llm.StopReasonStop {
		t.Fatalf("final recovered assistant = %#v", messages[3])
	}
}

func TestApplicationRejectsSessionWorkingDirectoryChange(t *testing.T) {
	t.Parallel()

	firstWorkspace := t.TempDir()
	secondWorkspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	model := &recordingModel{response: "answer"}

	newTestCommand := func() *cobra.Command {
		t.Helper()
		command, err := newCommand(dependencies{
			loadConfig: func() (config.Config, error) {
				return config.Config{DeepSeekAPIKey: "test-key"}, nil
			},
			newModel: func(config.Config) (agent.Model, error) {
				return model, nil
			},
		})
		if err != nil {
			t.Fatalf("newCommand() error = %v", err)
		}
		command.SetOut(io.Discard)
		return command
	}

	firstCommand := newTestCommand()
	firstCommand.SetArgs([]string{
		"--workspace", firstWorkspace,
		"--session", sessionPath,
		"--print", "first",
	})
	if err := firstCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("first ExecuteContext() error = %v", err)
	}

	secondCommand := newTestCommand()
	secondCommand.SetArgs([]string{
		"--workspace", secondWorkspace,
		"--session", sessionPath,
		"--print", "second",
	})
	err := secondCommand.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("second ExecuteContext() error = %v, want working-directory mismatch", err)
	}
}

func openSessionSnapshot(t *testing.T, path string) session.Snapshot {
	t.Helper()

	store, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("session.Open() error = %v", err)
	}
	snapshot, snapshotErr := store.Snapshot()
	closeErr := store.Close()
	if snapshotErr != nil || closeErr != nil {
		t.Fatalf(
			"session snapshot error = %v, close error = %v",
			snapshotErr,
			closeErr,
		)
	}
	return snapshot
}

func assertTextMessage(
	t *testing.T,
	message llm.AgentMessage,
	role llm.Role,
	text string,
) {
	t.Helper()

	switch role {
	case llm.RoleUser:
		value, ok := message.(llm.UserMessage)
		if !ok || len(value.Content) != 1 || value.Content[0].Text != text {
			t.Errorf("message = %#v, want user text %q", message, text)
		}
	case llm.RoleAssistant:
		value, ok := message.(llm.AssistantMessage)
		if !ok || len(value.Content) != 1 || value.Content[0].Text != text {
			t.Errorf("message = %#v, want assistant text %q", message, text)
		}
	default:
		t.Fatalf("unsupported expected role %q", role)
	}
}

func assertPersistedTerminalAssistant(
	t *testing.T,
	message llm.AssistantMessage,
	stopReason llm.StopReason,
) {
	t.Helper()

	if message.StopReason != stopReason ||
		message.ErrorMessage == "" ||
		len(message.Content) != 1 ||
		message.Content[0].Type != llm.ContentTypeText ||
		message.Content[0].Text != message.ErrorMessage {
		t.Fatalf(
			"terminal assistant = %#v, want %q with safe text",
			message,
			stopReason,
		)
	}
}

func persistedMessageRoles[T llm.AgentMessage](messages []T) []llm.Role {
	roles := make([]llm.Role, len(messages))
	for index, message := range messages {
		roles[index] = message.MessageRole()
	}
	return roles
}

func createAppTestSession(
	t *testing.T,
	path string,
	workingDirectory string,
) *session.Store {
	t.Helper()

	store, err := session.Create(t.Context(), path, session.Metadata{
		ID:               "test-session",
		CreatedAt:        1,
		WorkingDirectory: workingDirectory,
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}
	return store
}

type recordingModel struct {
	response string
	usage    llm.Usage
	requests []llm.Request
}

type toolLoopModel struct {
	requests  []llm.Request
	firstCall *llm.ToolCall
	secondErr error
}

func (m *toolLoopModel) Stream(
	_ context.Context,
	request llm.Request,
) (llm.Stream, error) {
	m.requests = append(m.requests, request)
	if len(m.requests) == 1 {
		call := llm.ToolCall{
			ID:        "call-1",
			Name:      "ls",
			Arguments: []byte(`{}`),
		}
		if m.firstCall != nil {
			call = *m.firstCall
		}
		message := llm.NewAssistantMessage(request.Model)
		message.Content = []llm.ContentPart{
			llm.NewTextContent("checking").Part(),
			{Type: llm.ContentTypeToolCall, ToolCall: &call},
		}
		message.StopReason = llm.StopReasonToolUse
		return &eventStream{events: []llm.Event{
			{Type: llm.EventTypeStart},
			{Type: llm.EventTypeTextStart, ContentIndex: 0},
			{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "checking"},
			{Type: llm.EventTypeTextEnd, ContentIndex: 0},
			{Type: llm.EventTypeToolCallStart, ContentIndex: 1},
			{Type: llm.EventTypeToolCallEnd, ContentIndex: 1, ToolCall: &call},
			{
				Type:       llm.EventTypeDone,
				StopReason: llm.StopReasonToolUse,
				Message:    &message,
			},
		}}, nil
	}
	if m.secondErr != nil {
		return nil, m.secondErr
	}

	message := llm.NewAssistantMessage(request.Model)
	message.Content = []llm.ContentPart{llm.NewTextContent("complete").Part()}
	message.StopReason = llm.StopReasonStop
	return &eventStream{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
		{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "complete"},
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{
			Type:       llm.EventTypeDone,
			StopReason: llm.StopReasonStop,
			Message:    &message,
		},
	}}, nil
}

func (m *recordingModel) Stream(
	_ context.Context,
	request llm.Request,
) (llm.Stream, error) {
	m.requests = append(m.requests, request)
	message := llm.NewAssistantMessage(request.Model)
	message.Content = []llm.ContentPart{llm.NewTextContent(m.response).Part()}
	message.Usage = m.usage
	message.StopReason = llm.StopReasonStop

	return &eventStream{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
		{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: m.response},
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{
			Type:       llm.EventTypeDone,
			StopReason: llm.StopReasonStop,
			Message:    &message,
		},
	}}, nil
}

type eventStream struct {
	events []llm.Event
	index  int
}

func (s *eventStream) Next() (llm.Event, error) {
	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *eventStream) Close() error {
	return nil
}

type appTestTool struct {
	definition llm.ToolDefinition
	execute    func(context.Context, llm.ToolCall) (llm.ToolResult, error)
}

func newAppTestTool(
	name string,
	execute func(context.Context, llm.ToolCall) (llm.ToolResult, error),
) *appTestTool {
	return &appTestTool{
		definition: llm.ToolDefinition{
			Name:        name,
			Description: "test tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		execute: execute,
	}
}

func (t *appTestTool) Definition() llm.ToolDefinition {
	return t.definition
}

func (t *appTestTool) Execute(
	ctx context.Context,
	call llm.ToolCall,
) (llm.ToolResult, error) {
	return t.execute(ctx, call)
}

func TestNewCommandRegistersUpdateCommand(t *testing.T) {
	t.Parallel()

	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		newModel: func(config.Config) (agent.Model, error) {
			return &recordingModel{}, nil
		},
	})
	if err != nil {
		t.Fatalf("newCommand() error = %v", err)
	}
	update, _, err := command.Find([]string{"update"})
	if err != nil {
		t.Fatalf("find update command: %v", err)
	}
	if update == nil {
		t.Fatal("update command not registered")
	}
}
