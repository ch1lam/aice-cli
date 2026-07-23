package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestApplicationPrintRunsReadOnlyAgent(t *testing.T) {
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
			if want := []string{"read", "ls", "grep", "find"}; !reflect.DeepEqual(toolNames, want) {
				t.Errorf("model tools = %v, want %v", toolNames, want)
			}
		})
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
		runTUI: func(
			ctx context.Context,
			runner tui.Runner,
			_ tui.Options,
		) error {
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

func TestApplicationDoesNotPersistFailedToolRun(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	wantErr := errors.New("provider disconnected")
	model := &toolLoopModel{secondErr: wantErr}
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

	snapshot := openSessionSnapshot(t, sessionPath)
	if len(snapshot.Turns) != 0 {
		t.Fatalf("persisted turns = %#v, want no incomplete run", snapshot.Turns)
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

type recordingModel struct {
	response string
	requests []llm.Request
}

type toolLoopModel struct {
	requests  []llm.Request
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
