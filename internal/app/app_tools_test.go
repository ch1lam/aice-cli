package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestApplicationPrintRunsMutatingBuiltInToolsThroughCommand(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	model := &builtInToolModel{}
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
	command.SetArgs([]string{
		"--workspace", workspace,
		"--print", "create and verify notes.txt",
	})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if got, want := output.String(), "completed\n"; got != want {
		t.Errorf("command output = %q, want %q", got, want)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(data), "beta\ngamma\n"; got != want {
		t.Errorf("notes.txt = %q, want %q", got, want)
	}

	if len(model.requests) != 5 {
		t.Fatalf("model requests = %d, want 5", len(model.requests))
	}
	wantTools := []string{"read", "write", "edit", "bash", "grep", "find", "ls"}
	for requestIndex, request := range model.requests {
		names := make([]string, len(request.Tools))
		for index, definition := range request.Tools {
			names[index] = definition.Name
		}
		if !reflect.DeepEqual(names, wantTools) {
			t.Errorf(
				"request %d tools = %v, want %v",
				requestIndex,
				names,
				wantTools,
			)
		}
	}
	if strings.Contains(
		strings.ToLower(model.requests[0].SystemPrompt),
		"read-only",
	) {
		t.Errorf("system prompt still describes read-only tools: %q", defaultSystemPrompt)
	}

	wantResults := []string{"write", "edit", "bash", "read"}
	for index, wantName := range wantResults {
		request := model.requests[index+1]
		result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
		if !ok {
			t.Fatalf(
				"request %d last message = %T, want ToolResultMessage",
				index+1,
				request.Messages[len(request.Messages)-1],
			)
		}
		if result.ToolName != wantName || result.IsError {
			t.Errorf(
				"request %d tool result = %#v, want successful %s",
				index+1,
				result,
				wantName,
			)
		}
		if wantName == "read" {
			text := toolResultText(t, result)
			if text != "beta\ngamma\n" {
				t.Errorf("read result = %q, want edited and appended content", text)
			}
		}
	}
}

func toolResultText(t *testing.T, result llm.ToolResultMessage) string {
	t.Helper()

	if len(result.Content) != 1 ||
		result.Content[0].Type != llm.ContentTypeText {
		t.Fatalf("tool result content = %#v, want one text part", result.Content)
	}
	return result.Content[0].Text
}

type builtInToolModel struct {
	requests []llm.Request
}

func (m *builtInToolModel) Stream(
	_ context.Context,
	request llm.Request,
) (llm.Stream, error) {
	m.requests = append(m.requests, request)
	calls := []llm.ToolCall{
		{
			ID:        "write-1",
			Name:      "write",
			Arguments: []byte(`{"path":"notes.txt","content":"alpha\n"}`),
		},
		{
			ID:   "edit-1",
			Name: "edit",
			Arguments: []byte(
				`{"path":"notes.txt","edits":[{"oldText":"alpha","newText":"beta"}]}`,
			),
		},
		{
			ID:        "bash-1",
			Name:      "bash",
			Arguments: []byte(`{"command":"printf '%s\\n' gamma >> notes.txt"}`),
		},
		{
			ID:        "read-1",
			Name:      "read",
			Arguments: []byte(`{"path":"notes.txt"}`),
		},
	}
	requestIndex := len(m.requests) - 1
	if requestIndex < len(calls) {
		return toolCallEventStream(request.Model, calls[requestIndex]), nil
	}

	message := llm.NewAssistantMessage(request.Model)
	message.Content = []llm.ContentPart{
		llm.NewTextContent("completed").Part(),
	}
	message.StopReason = llm.StopReasonStop
	return &eventStream{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeTextStart, ContentIndex: 0},
		{
			Type:         llm.EventTypeTextDelta,
			ContentIndex: 0,
			Delta:        "completed",
		},
		{Type: llm.EventTypeTextEnd, ContentIndex: 0},
		{
			Type:       llm.EventTypeDone,
			StopReason: llm.StopReasonStop,
			Message:    &message,
		},
	}}, nil
}

func toolCallEventStream(
	model llm.Model,
	call llm.ToolCall,
) llm.Stream {
	message := llm.NewAssistantMessage(model)
	message.Content = []llm.ContentPart{
		{Type: llm.ContentTypeToolCall, ToolCall: &call},
	}
	message.StopReason = llm.StopReasonToolUse
	return &eventStream{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{
			Type:         llm.EventTypeToolCallStart,
			ContentIndex: 0,
		},
		{
			Type:         llm.EventTypeToolCallEnd,
			ContentIndex: 0,
			ToolCall:     &call,
		},
		{
			Type:       llm.EventTypeDone,
			StopReason: llm.StopReasonToolUse,
			Message:    &message,
		},
	}}
}
