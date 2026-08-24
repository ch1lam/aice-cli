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
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
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
				data: `{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-flash-202607","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":2,"cache_read_input_tokens":3}}}`,
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
			ID:                      "deepseek-v4-flash",
			API:                     anthropicapi.API,
			Provider:                "deepseek",
			SupportsReasoningEffort: true,
			MaxTokens:               384_000,
			Pricing: llm.Pricing{
				Input:     0.14,
				Output:    0.28,
				CacheRead: 0.0028,
			},
		},
		SystemPrompt: "You are a coding agent.",
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "Read the file."}},
			},
			llm.AssistantMessage{
				Role:     llm.RoleAssistant,
				API:      anthropicapi.API,
				Provider: "deepseek",
				ModelID:  "deepseek-v4-flash",
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
			llm.ToolResultMessage{
				Role:       llm.RoleToolResult,
				ToolCallID: "call-1",
				Content: []llm.ContentPart{{
					Type: llm.ContentTypeText,
					Text: "file contents",
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

	events := apitest.CollectEvents(t, modelStream)
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
	wantUsage.Cost = llm.EstimateCost(request.Model.Pricing, wantUsage)
	if events[8].Usage == nil ||
		!reflect.DeepEqual(*events[8].Usage, wantUsage) {
		t.Errorf("usage event = %#v, want %#v", events[8].Usage, wantUsage)
	}
	done := events[9]
	if done.Message == nil {
		t.Fatal("done message is nil")
	}
	if !reflect.DeepEqual(done.Message.Usage, wantUsage) {
		t.Errorf("done message usage = %#v, want %#v", done.Message.Usage, wantUsage)
	}
	if done.StopReason != llm.StopReasonToolUse || done.Message.StopReason != llm.StopReasonToolUse {
		t.Errorf("done stop reasons = %q/%q, want %q", done.StopReason, done.Message.StopReason, llm.StopReasonToolUse)
	}
	if done.Message.Role != llm.RoleAssistant ||
		done.Message.API != anthropicapi.API ||
		done.Message.Provider != "deepseek" ||
		done.Message.ModelID != "deepseek-v4-flash" ||
		done.Message.ResponseModelID != "deepseek-v4-flash-202607" ||
		done.Message.ResponseID != "msg-1" ||
		done.Message.Timestamp == 0 {
		t.Errorf("done message metadata = %#v", done.Message)
	}
	wantContent := []llm.ContentPart{
		llm.NewThinkingContent("plan", "opaque-signature").Part(),
		{
			Type: llm.ContentTypeToolCall,
			ToolCall: &llm.ToolCall{
				ID:        "call-2",
				Name:      "read",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			},
		},
	}
	if !reflect.DeepEqual(done.Message.Content, wantContent) {
		t.Errorf("done message content = %#v, want %#v", done.Message.Content, wantContent)
	}

	body := <-requests
	assertRequestBody(t, body)
}

func TestAdapterDropsProtocolSignaturesForForeignAssistantHistory(t *testing.T) {
	t.Parallel()

	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
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

	request := llm.Request{
		Model: llm.Model{
			ID:        "deepseek-v4-pro",
			API:       anthropicapi.API,
			Provider:  "deepseek",
			MaxTokens: 1_000,
		},
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
			},
			llm.AssistantMessage{
				Role:     llm.RoleAssistant,
				API:      openairesponses.API,
				Provider: "deepseek",
				ModelID:  "deepseek-v4-flash",
				Content: []llm.ContentPart{
					llm.NewThinkingContent(
						"foreign plan",
						`{"id":"rs-1","type":"reasoning"}`,
					).Part(),
					{
						Type:      llm.ContentTypeText,
						Text:      "foreign answer",
						Signature: "msg-1",
					},
					{
						Type: llm.ContentTypeToolCall,
						ToolCall: &llm.ToolCall{
							ID:        "call-1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"README.md"}`),
							Signature: "fc-1",
						},
					},
				},
			},
			llm.ToolResultMessage{
				Role:       llm.RoleToolResult,
				ToolCallID: "call-1",
				Content:    []llm.ContentPart{llm.NewTextContent("contents").Part()},
			},
		},
	}

	modelStream, err := adapter.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := modelStream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	body := <-requests
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v, want user, assistant, tool result", body["messages"])
	}
	assistant := messages[1].(map[string]any)
	content, ok := assistant["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf(
			"assistant content = %#v, want two text blocks and tool use",
			assistant["content"],
		)
	}
	first := content[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "foreign plan" {
		t.Errorf("foreign thinking conversion = %#v", first)
	}
	for _, block := range content {
		if block.(map[string]any)["type"] == "thinking" {
			t.Errorf("foreign thinking retained protocol signature: %#v", content)
		}
	}
}

func TestAdapterRejectsExcessiveTemperature(t *testing.T) {
	t.Parallel()

	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unexpected HTTP request")
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := apitest.MinimalRequest(anthropicapi.API)
	temperature := 2.5
	request.Options.Temperature = &temperature
	_, err = adapter.Stream(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "temperature cannot exceed 2") {
		t.Fatalf("Stream() error = %v, want temperature rejection", err)
	}
}

func TestAdapterEmptyToolCallArgumentsBecomeEmptyObject(t *testing.T) {
	t.Parallel()

	server := apitest.NewRawSSEServer(t, []string{
		`event: message_start\ndata: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-flash","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}\n\n`,
		`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-1","name":"list"}}\n\n`,
		`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n`,
		`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":1}}\n\n`,
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
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(anthropicapi.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
	last := events[len(events)-1]
	if last.Type != llm.EventTypeDone || last.Message == nil ||
		len(last.Message.Content) != 1 {
		t.Fatalf("last event = %#v, want done with one tool call", last)
	}
	call := last.Message.Content[0].ToolCall
	if call == nil || string(call.Arguments) != "{}" {
		t.Errorf("tool call arguments = %#v, want {}", call)
	}
}

func TestAdapterGroupsConsecutiveToolResultsInOneUserMessage(t *testing.T) {
	t.Parallel()

	requests := make(chan map[string]any, 1)
	client := &http.Client{Transport: apitest.RoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		requests <- body
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: request,
		}, nil
	})}
	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     "test-key",
		BaseURL:    "https://example.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	toolCalls := []llm.ToolCall{
		{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		{ID: "call-2", Name: "read", Arguments: json.RawMessage(`{"path":"b.go"}`)},
		{ID: "call-3", Name: "ls", Arguments: json.RawMessage(`{}`)},
	}
	request := apitest.MinimalRequest(anthropicapi.API)
	request.Messages = []llm.Message{
		request.Messages[0],
		llm.AssistantMessage{
			Role:     llm.RoleAssistant,
			API:      anthropicapi.API,
			Provider: "deepseek",
			ModelID:  "deepseek-v4-flash",
			Content: []llm.ContentPart{
				{Type: llm.ContentTypeToolCall, ToolCall: &toolCalls[0]},
				{Type: llm.ContentTypeToolCall, ToolCall: &toolCalls[1]},
				{Type: llm.ContentTypeToolCall, ToolCall: &toolCalls[2]},
			},
		},
	}
	for _, call := range toolCalls {
		result := llm.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: []llm.ContentPart{llm.NewTextContent("result:" + call.ID).Part()},
		}
		message, err := llm.NewToolResultMessage(result)
		if err != nil {
			t.Fatalf("NewToolResultMessage() error = %v", err)
		}
		request.Messages = append(request.Messages, message)
	}

	modelStream, err := adapter.Stream(t.Context(), request)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := modelStream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	body := <-requests
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v, want user, assistant, and one tool-result user message", body["messages"])
	}
	toolMessage, ok := messages[2].(map[string]any)
	if !ok || toolMessage["role"] != "user" {
		t.Fatalf("tool message = %#v, want user role", messages[2])
	}
	blocks, ok := toolMessage["content"].([]any)
	if !ok || len(blocks) != len(toolCalls) {
		t.Fatalf("tool-result blocks = %#v, want %d blocks", toolMessage["content"], len(toolCalls))
	}
	for index, blockValue := range blocks {
		block, ok := blockValue.(map[string]any)
		if !ok || block["type"] != "tool_result" || block["tool_use_id"] != toolCalls[index].ID {
			t.Errorf("tool-result block %d = %#v, want id %q", index, blockValue, toolCalls[index].ID)
		}
	}
}

func TestAdapterRejectsIncompleteStreamedToolCall(t *testing.T) {
	t.Parallel()

	server := apitest.NewRawSSEServer(t, []string{
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
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(anthropicapi.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
	if len(events) == 0 || events[len(events)-1].Type != llm.EventTypeError {
		t.Fatalf("last event = %#v, want error event", events)
	}
	terminal := events[len(events)-1]
	if terminal.Message == nil || !strings.Contains(terminal.Message.ErrorMessage, "invalid JSON") {
		t.Fatalf("error message = %#v, want invalid JSON error", terminal.Message)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "invalid JSON") {
		t.Fatalf("terminal error = %v, want invalid JSON error", terminal.Err)
	}
	if terminal.StopReason != llm.StopReasonError || terminal.Message.StopReason != llm.StopReasonError {
		t.Errorf("error stop reasons = %q/%q", terminal.StopReason, terminal.Message.StopReason)
	}
}

func TestAdapterPreservesPartialTextOnStreamError(t *testing.T) {
	t.Parallel()

	server := apitest.NewRawSSEServer(t, []string{
		`event: message_start\ndata: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-flash","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}\n\n`,
		`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n`,
		`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}\n\n`,
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
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(anthropicapi.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
	terminal := events[len(events)-1]
	if terminal.Type != llm.EventTypeError || terminal.Message == nil {
		t.Fatalf("terminal event = %#v", terminal)
	}
	if terminal.Err == nil || terminal.Message.ErrorMessage != "anthropic: model stream ended unexpectedly" {
		t.Errorf("terminal error = %v, message = %q", terminal.Err, terminal.Message.ErrorMessage)
	}
	wantContent := []llm.ContentPart{llm.NewTextContent("partial").Part()}
	if !reflect.DeepEqual(terminal.Message.Content, wantContent) {
		t.Errorf("partial content = %#v, want %#v", terminal.Message.Content, wantContent)
	}
}

func TestAdapterStreamsText(t *testing.T) {
	t.Parallel()

	server := apitest.NewRawSSEServer(t, []string{
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
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(anthropicapi.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
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
	if events[5].Message == nil {
		t.Fatal("done message is nil")
	}
	wantContent := []llm.ContentPart{llm.NewTextContent("hello").Part()}
	if !reflect.DeepEqual(events[5].Message.Content, wantContent) {
		t.Errorf("done message content = %#v, want %#v", events[5].Message.Content, wantContent)
	}
}

func TestAdapterThinkingLevelsFromModelMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		level            llm.ThinkingLevel
		thinkingLevelMap llm.ThinkingLevelMap
		wantEffort       string
		wantDisabled     bool
		budgetOnly       bool
		wantErr          string
	}{
		{
			name:       "deepseek pro low",
			level:      llm.ThinkingLevelLow,
			wantEffort: "low",
		},
		{
			name:       "deepseek pro high",
			level:      llm.ThinkingLevelHigh,
			wantEffort: "high",
		},
		{
			name:             "canonical minimal collapses to low",
			level:            llm.ThinkingLevelMinimal,
			thinkingLevelMap: llm.ThinkingLevelMap{},
			wantEffort:       "low",
		},
		{
			name:       "deepseek pro max",
			level:      llm.ThinkingLevelMax,
			wantEffort: "max",
		},
		{
			name:         "deepseek pro off disables thinking",
			level:        llm.ThinkingLevelOff,
			wantDisabled: true,
		},
		{
			name:  "explicit unsupported off rejected",
			level: llm.ThinkingLevelOff,
			thinkingLevelMap: llm.ThinkingLevelMap{
				llm.ThinkingLevelOff: nil,
			},
			wantErr: `model "deepseek-v4-pro" does not support thinking level "off"`,
		},
		{
			name:       "deepseek pro maps medium to high",
			level:      llm.ThinkingLevelMedium,
			wantEffort: "high",
		},
		{
			name:       "deepseek pro maps xhigh to high",
			level:      llm.ThinkingLevelXHigh,
			wantEffort: "high",
		},
		{
			name:       "budget-only model omits effort",
			level:      llm.ThinkingLevelHigh,
			budgetOnly: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request body: %v", err)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "")
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

			request := apitest.MinimalRequest(anthropicapi.API)
			request.Model = anthropicModelForTest(t, deepseek.ModelV4Pro)
			if tt.budgetOnly {
				request.Model.SupportsReasoningEffort = false
			}
			if tt.thinkingLevelMap != nil {
				request.Model.ThinkingLevelMap = tt.thinkingLevelMap
			}
			request.Options.Thinking = tt.level
			modelStream, err := adapter.Stream(context.Background(), request)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Stream() error = %v, want text %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			defer func() {
				if err := modelStream.Close(); err != nil {
					t.Errorf("Close() error = %v", err)
				}
			}()

			body := <-requests
			thinking, ok := body["thinking"].(map[string]any)
			if !ok {
				t.Fatalf("thinking = %#v, want an object", body["thinking"])
			}
			if tt.wantDisabled {
				if thinking["type"] != "disabled" {
					t.Errorf("thinking type = %#v, want disabled", thinking["type"])
				}
				if _, present := body["output_config"]; present {
					t.Errorf("output_config unexpectedly present: %#v", body["output_config"])
				}
				return
			}
			if thinking["type"] != "enabled" {
				t.Errorf("thinking type = %#v, want enabled", thinking["type"])
			}
			if tt.budgetOnly {
				if _, present := body["output_config"]; present {
					t.Errorf("output_config unexpectedly present: %#v", body["output_config"])
				}
				return
			}
			outputConfig, ok := body["output_config"].(map[string]any)
			if !ok || outputConfig["effort"] != tt.wantEffort {
				t.Errorf("output_config = %#v, want effort %q", body["output_config"], tt.wantEffort)
			}
		})
	}
}

func anthropicModelForTest(t *testing.T, id string) llm.Model {
	t.Helper()

	for _, model := range deepseek.Models() {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("deepseek model %q missing from catalog", id)
	return llm.Model{}
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
	_, err = adapter.Stream(ctx, apitest.MinimalRequest(anthropicapi.API))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
}

func TestAdapterRejectsInvalidRequestBeforeHTTP(t *testing.T) {
	t.Parallel()

	requestSent := false
	client := &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		requestSent = true
		return nil, errors.New("unexpected HTTP request")
	})}
	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     "test-key",
		BaseURL:    "https://example.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := apitest.MinimalRequest(anthropicapi.API)
	request.Messages = nil
	_, err = adapter.Stream(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "validate request") ||
		!strings.Contains(err.Error(), "at least one message is required") {
		t.Fatalf("Stream() error = %v, want request validation error", err)
	}
	if requestSent {
		t.Fatal("Stream() sent an HTTP request before validation completed")
	}
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
