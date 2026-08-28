package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestStreamPrinterSeparatesResponseAndProgress(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	diagnostics := new(bytes.Buffer)
	printer := newStreamPrinter(output, diagnostics)
	currentTime := time.Unix(10, 0)
	printer.now = func() time.Time { return currentTime }

	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test\n./..."}`),
	}
	usage := llm.Usage{
		InputTokens:      10,
		OutputTokens:     4,
		ReasoningTokens:  2,
		CacheReadTokens:  3,
		CacheWriteTokens: 1,
		TotalTokens:      20,
		Cost:             &llm.Cost{Total: 0.125},
	}
	assistant := llm.NewAssistantMessage(llm.Model{ID: "test-model"})
	assistant.StopReason = llm.StopReasonToolUse
	assistant.Usage = usage
	toolResult, err := llm.NewToolResultMessage(llm.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: []llm.ContentPart{llm.NewTextContent("denied").Part()},
		IsError: true,
	})
	if err != nil {
		t.Fatalf("NewToolResultMessage() error = %v", err)
	}

	events := []agent.AgentEvent{
		{
			Type: agent.EventTypeMessageUpdate,
			AssistantMessageEvent: &llm.Event{
				Type:  llm.EventTypeThinkingDelta,
				Delta: "hidden",
			},
		},
		{
			Type: agent.EventTypeMessageUpdate,
			AssistantMessageEvent: &llm.Event{
				Type:  llm.EventTypeTextDelta,
				Delta: "answer",
			},
		},
		{Type: agent.EventTypeMessageEnd, Message: assistant},
		{Type: agent.EventTypeToolExecutionStart, ToolCall: &call},
	}
	for _, event := range events {
		if err := printer.Accept(t.Context(), event); err != nil {
			t.Fatalf("Accept(%q) error = %v", event.Type, err)
		}
	}

	currentTime = currentTime.Add(125 * time.Millisecond)
	remaining := []agent.AgentEvent{
		{
			Type:       agent.EventTypeToolExecutionEnd,
			ToolCall:   &call,
			ToolResult: &toolResult,
		},
		{
			Type: agent.EventTypeRetryStart,
			Retry: &agent.RetryEvent{
				Attempt:    1,
				MaxRetries: 3,
				Delay:      2 * time.Second,
			},
		},
		{
			Type: agent.EventTypeRetryEnd,
			Retry: &agent.RetryEvent{
				Attempt:    1,
				MaxRetries: 3,
				Success:    true,
			},
		},
	}
	for _, event := range remaining {
		if err := printer.Accept(t.Context(), event); err != nil {
			t.Fatalf("Accept(%q) error = %v", event.Type, err)
		}
	}
	if err := printer.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if err := printer.Finish(); err != nil {
		t.Fatalf("second Finish() error = %v", err)
	}

	if got, want := output.String(), "answer\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	wantProgress := "aice: assistant stop_reason=tool_use " + formatUsage(usage) + "\n" +
		"aice: tool name=bash status=started detail=\"go test ./...\"\n" +
		"aice: tool name=bash status=failed duration_ms=125\n" +
		"aice: retry attempt=1 max_retries=3 status=waiting delay_ms=2000\n" +
		"aice: retry attempt=1 max_retries=3 status=succeeded\n" +
		"aice: total " + formatUsage(usage) + "\n"
	if got := diagnostics.String(); got != wantProgress {
		t.Errorf("stderr = %q, want %q", got, wantProgress)
	}
}

func TestJSONPrinterEmitsStableEvents(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	printer := newJSONPrinter(output)
	currentTime := time.Unix(20, 0)
	printer.now = func() time.Time { return currentTime }

	call := llm.ToolCall{
		ID:        "call-1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	}
	usage := llm.Usage{InputTokens: 8, OutputTokens: 3, TotalTokens: 11}
	assistant := llm.NewAssistantMessage(llm.Model{ID: "requested-model"})
	assistant.ResponseModelID = "response-model"
	assistant.Content = []llm.ContentPart{
		llm.NewThinkingContent("reasoning", "").Part(),
		llm.NewTextContent("answer").Part(),
	}
	assistant.Usage = usage
	assistant.StopReason = llm.StopReasonToolUse
	toolResult, err := llm.NewToolResultMessage(llm.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: []llm.ContentPart{llm.NewTextContent("contents").Part()},
		IsError: true,
	})
	if err != nil {
		t.Fatalf("NewToolResultMessage() error = %v", err)
	}
	wantRunErr := errors.New("provider unavailable")

	events := []agent.AgentEvent{
		{Type: agent.EventTypeAgentStart},
		{Type: agent.EventTypeMessageEnd, Message: assistant},
		{Type: agent.EventTypeMessageEnd, Message: toolResult},
		{Type: agent.EventTypeToolExecutionStart, ToolCall: &call},
	}
	for _, event := range events {
		if err := printer.Accept(t.Context(), event); err != nil {
			t.Fatalf("Accept(%q) error = %v", event.Type, err)
		}
	}
	currentTime = currentTime.Add(42 * time.Millisecond)
	remaining := []agent.AgentEvent{
		{
			Type:       agent.EventTypeToolExecutionEnd,
			ToolCall:   &call,
			ToolResult: &toolResult,
		},
		{
			Type: agent.EventTypeRetryStart,
			Retry: &agent.RetryEvent{
				Attempt:    2,
				MaxRetries: 3,
				Delay:      4 * time.Second,
			},
		},
		{
			Type: agent.EventTypeRetryEnd,
			Retry: &agent.RetryEvent{
				Attempt:    2,
				MaxRetries: 3,
				Err:        wantRunErr,
			},
		},
		{Type: agent.EventTypeAgentEnd, Err: wantRunErr},
	}
	for _, event := range remaining {
		if err := printer.Accept(t.Context(), event); err != nil {
			t.Fatalf("Accept(%q) error = %v", event.Type, err)
		}
	}
	if err := printer.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	lines := decodeJSONLines(t, output.String())
	if got, want := len(lines), 7; got != want {
		t.Fatalf("JSON events = %d, want %d: %s", got, want, output.String())
	}
	wantTypes := []string{
		"agent_start",
		"message_end",
		"tool_execution_start",
		"tool_execution_end",
		"retry_start",
		"retry_end",
		"agent_end",
	}
	for index, want := range wantTypes {
		if got := lines[index]["type"]; got != want {
			t.Errorf("event %d type = %v, want %q", index, got, want)
		}
	}
	message := lines[1]
	if message["text"] != "answer" ||
		message["thinking"] != "reasoning" ||
		message["model"] != "response-model" {
		t.Errorf("message_end = %#v", message)
	}
	toolEnd := lines[3]
	if toolEnd["is_error"] != true ||
		toolEnd["result"] != "contents" ||
		toolEnd["duration_ms"] != float64(42) {
		t.Errorf("tool_execution_end = %#v", toolEnd)
	}
	agentEnd := lines[6]
	if agentEnd["error"] != wantRunErr.Error() {
		t.Errorf("agent_end error = %v, want %q", agentEnd["error"], wantRunErr)
	}
	gotUsage, ok := agentEnd["usage"].(map[string]any)
	if !ok || gotUsage["input_tokens"] != float64(usage.InputTokens) {
		t.Errorf("agent_end usage = %#v, want %#v", agentEnd["usage"], usage)
	}
}

func TestJSONPrinterTruncatesToolResults(t *testing.T) {
	t.Parallel()

	output := new(bytes.Buffer)
	printer := newJSONPrinter(output)
	call := llm.ToolCall{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{}`)}
	result, err := llm.NewToolResultMessage(llm.ToolResult{
		CallID: call.ID,
		Name:   call.Name,
		Content: []llm.ContentPart{
			llm.NewTextContent(strings.Repeat("界", maxJSONToolResultBytes)).Part(),
		},
	})
	if err != nil {
		t.Fatalf("NewToolResultMessage() error = %v", err)
	}
	if err := printer.Accept(t.Context(), agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionEnd,
		ToolCall:   &call,
		ToolResult: &result,
	}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	lines := decodeJSONLines(t, output.String())
	got, ok := lines[0]["result"].(string)
	if !ok {
		t.Fatalf("result = %#v, want string", lines[0]["result"])
	}
	if len(got) > maxJSONToolResultBytes || !strings.HasSuffix(got, toolResultTruncation) {
		t.Errorf("truncated result bytes = %d, suffix = %q", len(got), got[len(got)-len(toolResultTruncation):])
	}
}

func TestPrintSinksStopOnWriteFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("write failed")
	tests := []struct {
		name  string
		sink  printSink
		event agent.AgentEvent
	}{
		{
			name: "text progress",
			sink: newStreamPrinter(io.Discard, errorWriter{err: wantErr}),
			event: agent.AgentEvent{
				Type: agent.EventTypeToolExecutionStart,
				ToolCall: &llm.ToolCall{
					ID:        "call-1",
					Name:      "read",
					Arguments: json.RawMessage(`{}`),
				},
			},
		},
		{
			name:  "json event",
			sink:  newJSONPrinter(errorWriter{err: wantErr}),
			event: agent.AgentEvent{Type: agent.EventTypeAgentStart},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.sink.Accept(t.Context(), test.event)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Accept() error = %v, want %v", err, wantErr)
			}
		})
	}
}

func decodeJSONLines(t *testing.T, output string) []map[string]any {
	t.Helper()

	decoder := json.NewDecoder(strings.NewReader(output))
	lines := make([]map[string]any, 0)
	for {
		var line map[string]any
		if err := decoder.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				return lines
			}
			t.Fatalf("Decode() error = %v, output = %q", err, output)
		}
		lines = append(lines, line)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
