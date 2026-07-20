package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	anthropicapi "github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterStreamsThinkingToolCallUsageAndDone(t *testing.T) {
	t.Parallel()

	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("request path = %q, want %q", r.URL.Path, "/v1/messages")
		}
		if got := r.Header.Get("X-Api-Key"); got != "test-key" {
			t.Errorf("X-Api-Key = %q, want %q", got, "test-key")
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		requests <- body

		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []struct {
			name string
			data string
		}{
			{
				name: "message_start",
				data: `{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-flash","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`,
			},
			{
				name: "content_block_start",
				data: `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			},
			{
				name: "content_block_delta",
				data: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan"}}`,
			},
			{
				name: "content_block_delta",
				data: `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}`,
			},
			{
				name: "content_block_stop",
				data: `{"type":"content_block_stop","index":0}`,
			},
			{
				name: "content_block_start",
				data: `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-2","name":"read","input":{}}}`,
			},
			{
				name: "content_block_delta",
				data: `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			},
			{
				name: "content_block_delta",
				data: `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"README.md\"}"}}`,
			},
			{
				name: "content_block_stop",
				data: `{"type":"content_block_stop","index":1}`,
			},
			{
				name: "message_delta",
				data: `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":7,"output_tokens_details":{"thinking_tokens":2}}}`,
			},
			{name: "message_stop", data: `{"type":"message_stop"}`},
		} {
			_, _ = io.WriteString(w, "event: "+event.name+"\n")
			_, _ = io.WriteString(w, "data: "+event.data+"\n\n")
		}
	}))
	defer server.Close()

	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	temperature := 0.2
	request := llm.Request{
		Model: llm.Model{
			ID:        "deepseek-v4-flash",
			API:       anthropicapi.API,
			Provider:  "deepseek",
			MaxTokens: 384_000,
		},
		SystemPrompt: "You are a coding agent.",
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "Read the file."}},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{
						Type:      llm.ContentTypeThinking,
						Text:      "I should inspect it.",
						Signature: "previous-signature",
					},
					{
						Type: llm.ContentTypeToolCall,
						ToolCall: &llm.ToolCall{
							ID:        "call-1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"AGENTS.md"}`),
						},
					},
				},
			},
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{{
					Type: llm.ContentTypeToolResult,
					ToolResult: &llm.ToolResult{
						CallID: "call-1",
						Content: []llm.ContentPart{{
							Type: llm.ContentTypeText,
							Text: "file contents",
						}},
					},
				}},
			},
		},
		Tools: []llm.ToolDefinition{{
			Name:        "read",
			Description: "Read a file.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		}},
		Options: llm.StreamOptions{
			Temperature: &temperature,
			MaxTokens:   4_096,
			Thinking:    llm.ThinkingLevelHigh,
		},
	}

	modelStream, err := adapter.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	events := collectEvents(t, modelStream)
	wantTypes := []llm.EventType{
		llm.EventTypeStart,
		llm.EventTypeThinkingStart,
		llm.EventTypeThinkingDelta,
		llm.EventTypeThinkingEnd,
		llm.EventTypeToolCallStart,
		llm.EventTypeToolCallDelta,
		llm.EventTypeToolCallDelta,
		llm.EventTypeToolCallEnd,
		llm.EventTypeUsage,
		llm.EventTypeDone,
	}
	gotTypes := make([]llm.EventType, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}

	thinking := events[3].Content
	if thinking == nil || thinking.Text != "plan" || thinking.Signature != "opaque-signature" {
		t.Errorf("thinking end content = %#v", thinking)
	}
	toolCall := events[7].ToolCall
	if toolCall == nil || toolCall.ID != "call-2" || toolCall.Name != "read" ||
		string(toolCall.Arguments) != `{"path":"README.md"}` {
		t.Errorf("tool call end = %#v", toolCall)
	}

	wantUsage := llm.Usage{
		InputTokens:      10,
		OutputTokens:     7,
		ReasoningTokens:  2,
		CacheReadTokens:  3,
		CacheWriteTokens: 2,
		TotalTokens:      22,
	}
	if events[9].Usage == nil || !reflect.DeepEqual(*events[9].Usage, wantUsage) {
		t.Errorf("done usage = %#v, want %#v", events[9].Usage, wantUsage)
	}
	if events[9].StopReason != llm.StopReasonToolUse {
		t.Errorf("done stop reason = %q, want %q", events[9].StopReason, llm.StopReasonToolUse)
	}

	body := <-requests
	assertRequestBody(t, body)
}

func TestAdapterRejectsIncompleteStreamedToolCall(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t, []string{
		`event: message_start\ndata: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-flash","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}\n\n`,
		`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"read","input":{}}}\n\n`,
		`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}\n\n`,
		`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n`,
	})
	defer server.Close()

	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	modelStream, err := adapter.Stream(context.Background(), minimalRequest())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	for {
		_, err = modelStream.Next()
		if err != nil {
			break
		}
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("Next() error = %v, want invalid JSON error", err)
	}
}

func TestAdapterStreamsText(t *testing.T) {
	t.Parallel()

	server := newSSEServer(t, []string{
		`event: message_start\ndata: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-flash","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}\n\n`,
		`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n`,
		`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}\n\n`,
		`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n`,
		`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}\n\n`,
		`event: message_stop\ndata: {"type":"message_stop"}\n\n`,
	})
	defer server.Close()

	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	modelStream, err := adapter.Stream(context.Background(), minimalRequest())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := collectEvents(t, modelStream)
	wantTypes := []llm.EventType{
		llm.EventTypeStart,
		llm.EventTypeTextStart,
		llm.EventTypeTextDelta,
		llm.EventTypeTextEnd,
		llm.EventTypeUsage,
		llm.EventTypeDone,
	}
	gotTypes := make([]llm.EventType, 0, len(events))
	for _, event := range events {
		gotTypes = append(gotTypes, event.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}
	if events[2].Delta != "hello" {
		t.Errorf("text delta = %q, want %q", events[2].Delta, "hello")
	}
	if events[3].Content == nil || events[3].Content.Text != "hello" {
		t.Errorf("text end content = %#v", events[3].Content)
	}
	if events[5].StopReason != llm.StopReasonStop {
		t.Errorf("stop reason = %q, want %q", events[5].StopReason, llm.StopReasonStop)
	}
}

func TestAdapterPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.Stream(ctx, minimalRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
}

func collectEvents(t *testing.T, stream llm.Stream) []llm.Event {
	t.Helper()

	var events []llm.Event
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		events = append(events, event)
	}
}

func minimalRequest() llm.Request {
	return llm.Request{
		Model: llm.Model{
			ID:        "deepseek-v4-flash",
			API:       anthropicapi.API,
			Provider:  "deepseek",
			MaxTokens: 1_000,
		},
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{{
				Type: llm.ContentTypeText,
				Text: "hello",
			}},
		}},
	}
}

func newSSEServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, strings.ReplaceAll(chunk, `\n`, "\n"))
		}
	}))
}

func assertRequestBody(t *testing.T, body map[string]any) {
	t.Helper()

	if body["model"] != "deepseek-v4-flash" {
		t.Errorf("model = %#v", body["model"])
	}
	if body["max_tokens"] != float64(4_096) {
		t.Errorf("max_tokens = %#v", body["max_tokens"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %#v", body["stream"])
	}
	if body["temperature"] != 0.2 {
		t.Errorf("temperature = %#v", body["temperature"])
	}

	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(1_024) {
		t.Errorf("thinking = %#v", body["thinking"])
	}
	outputConfig, ok := body["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != "high" {
		t.Errorf("output_config = %#v", body["output_config"])
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v", body["messages"])
	}
	toolMessage := messages[2].(map[string]any)
	if toolMessage["role"] != "user" {
		t.Errorf("tool message role = %#v", toolMessage["role"])
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "read" || tool["description"] != "Read a file." {
		t.Errorf("tool = %#v", tool)
	}
}
