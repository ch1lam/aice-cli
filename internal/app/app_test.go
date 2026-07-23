package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
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
}

type recordingModel struct {
	response string
	requests []llm.Request
}

type toolLoopModel struct {
	requests []llm.Request
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
