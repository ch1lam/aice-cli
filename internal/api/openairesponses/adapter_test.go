package openairesponses_test

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

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterStreamsReasoningTextToolCallUsageAndDone(t *testing.T) {
	t.Parallel()

	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("request path = %q, want %q", r.URL.Path, "/responses")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		requests <- body

		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.created","sequence_number":0,"response":{"id":"resp-1","model":"deepseek-v4-flash-202607","status":"in_progress"}}`,
			`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"rs-1","type":"reasoning","summary":[],"content":[],"status":"in_progress"}}`,
			`{"type":"response.reasoning_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"rs-1","delta":"plan"}`,
			`{"type":"response.reasoning_text.done","sequence_number":3,"output_index":0,"content_index":0,"item_id":"rs-1","text":"plan"}`,
			`{"type":"response.output_item.done","sequence_number":4,"output_index":0,"item":{"id":"rs-1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"plan"}],"status":"completed"}}`,
			`{"type":"response.output_item.added","sequence_number":5,"output_index":1,"item":{"id":"msg-1","type":"message","role":"assistant","content":[],"status":"in_progress"}}`,
			`{"type":"response.output_text.delta","sequence_number":6,"output_index":1,"content_index":0,"item_id":"msg-1","delta":"hello","logprobs":[]}`,
			`{"type":"response.output_item.done","sequence_number":7,"output_index":1,"item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}],"status":"completed"}}`,
			`{"type":"response.output_item.added","sequence_number":8,"output_index":2,"item":{"id":"fc-1","type":"function_call","call_id":"call-2","name":"read","arguments":"","status":"in_progress"}}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":9,"output_index":2,"item_id":"fc-1","delta":"{\"path\":"}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":10,"output_index":2,"item_id":"fc-1","delta":"\"README.md\"}"}`,
			`{"type":"response.function_call_arguments.done","sequence_number":11,"output_index":2,"item_id":"fc-1","name":"read","arguments":"{\"path\":\"README.md\"}"}`,
			`{"type":"response.output_item.done","sequence_number":12,"output_index":2,"item":{"id":"fc-1","type":"function_call","call_id":"call-2","name":"read","arguments":"{\"path\":\"README.md\"}","status":"completed"}}`,
			`{"type":"response.completed","sequence_number":13,"response":{"id":"resp-1","model":"deepseek-v4-flash-202607","status":"completed","usage":{"input_tokens":15,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":2},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":22}}}`,
		} {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
	defer server.Close()

	adapter, err := openairesponses.New(openairesponses.Config{
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
			API:       openairesponses.API,
			Provider:  "deepseek",
			MaxTokens: 384_000,
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
				Content: []llm.ContentPart{llm.NewTextContent("Read the file.").Part()},
			},
			llm.AssistantMessage{
				Role:     llm.RoleAssistant,
				API:      openairesponses.API,
				Provider: "deepseek",
				ModelID:  "deepseek-v4-flash",
				Content: []llm.ContentPart{
					llm.NewThinkingContent(
						"I should inspect it.",
						`{"id":"rs-prev","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"I should inspect it."}],"status":"completed"}`,
					).Part(),
					{
						Type:      llm.ContentTypeText,
						Text:      "I will read it.",
						Signature: "msg-prev",
					},
					{
						Type: llm.ContentTypeToolCall,
						ToolCall: &llm.ToolCall{
							ID:        "call-1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"AGENTS.md"}`),
							Signature: "fc-prev",
						},
					},
				},
			},
			llm.ToolResultMessage{
				Role:       llm.RoleToolResult,
				ToolCallID: "call-1",
				Content:    []llm.ContentPart{llm.NewTextContent("file contents").Part()},
			},
		},
		Tools: []llm.ToolDefinition{{
			Name:        "read",
			Description: "Read a file.",
			InputSchema: json.RawMessage(
				`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			),
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
		llm.EventTypeTextStart,
		llm.EventTypeTextDelta,
		llm.EventTypeTextEnd,
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
	if thinking == nil || thinking.Text != "plan" {
		t.Fatalf("thinking end content = %#v", thinking)
	}
	var reasoningSignature map[string]any
	if err := json.Unmarshal([]byte(thinking.Signature), &reasoningSignature); err != nil {
		t.Fatalf("decode reasoning signature: %v", err)
	}
	if reasoningSignature["id"] != "rs-1" {
		t.Errorf("reasoning signature id = %#v, want %q", reasoningSignature["id"], "rs-1")
	}

	text := events[6].Content
	if text == nil || text.Text != "hello" || text.Signature != "msg-1" {
		t.Errorf("text end content = %#v", text)
	}
	toolCall := events[10].ToolCall
	if toolCall == nil ||
		toolCall.ID != "call-2" ||
		toolCall.Name != "read" ||
		toolCall.Signature != "fc-1" ||
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
	if events[11].Usage == nil || !reflect.DeepEqual(*events[11].Usage, wantUsage) {
		t.Errorf("usage event = %#v, want %#v", events[11].Usage, wantUsage)
	}

	done := events[12]
	if done.Message == nil {
		t.Fatal("done message is nil")
	}
	if done.StopReason != llm.StopReasonToolUse ||
		done.Message.StopReason != llm.StopReasonToolUse {
		t.Errorf(
			"done stop reasons = %q/%q, want %q",
			done.StopReason,
			done.Message.StopReason,
			llm.StopReasonToolUse,
		)
	}
	if done.Message.API != openairesponses.API ||
		done.Message.Provider != "deepseek" ||
		done.Message.ModelID != "deepseek-v4-flash" ||
		done.Message.ResponseModelID != "deepseek-v4-flash-202607" ||
		done.Message.ResponseID != "resp-1" ||
		done.Message.Timestamp == 0 {
		t.Errorf("done message metadata = %#v", done.Message)
	}
	if len(done.Message.Content) != 3 {
		t.Fatalf("done message content = %#v, want three parts", done.Message.Content)
	}

	body := <-requests
	assertRequestBody(t, body)
}

func TestAdapterRejectsIncompleteStreamedToolCall(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp-1","model":"deepseek-v4-flash","status":"in_progress"}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"read","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":2,"output_index":0,"item_id":"fc-1","delta":"{"}`,
		`{"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"read","arguments":"{","status":"incomplete"}}`,
	})
	defer server.Close()

	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(openairesponses.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
	terminal := events[len(events)-1]
	if terminal.Type != llm.EventTypeError ||
		terminal.Message == nil ||
		!strings.Contains(terminal.Message.ErrorMessage, "invalid JSON") {
		t.Fatalf("terminal event = %#v, want invalid JSON error", terminal)
	}
}

func TestAdapterPreservesPartialTextOnUnexpectedEOF(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp-1","model":"deepseek-v4-flash","status":"in_progress"}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg-1","type":"message","role":"assistant","content":[],"status":"in_progress"}}`,
		`{"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg-1","delta":"partial","logprobs":[]}`,
	})
	defer server.Close()

	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(openairesponses.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
	terminal := events[len(events)-1]
	if terminal.Type != llm.EventTypeError || terminal.Message == nil {
		t.Fatalf("terminal event = %#v", terminal)
	}
	want := []llm.ContentPart{{
		Type:      llm.ContentTypeText,
		Text:      "partial",
		Signature: "msg-1",
	}}
	if !reflect.DeepEqual(terminal.Message.Content, want) {
		t.Errorf("partial content = %#v, want %#v", terminal.Message.Content, want)
	}
}

func TestAdapterPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.Stream(ctx, apitest.MinimalRequest(openairesponses.API))
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
	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     "test-key",
		BaseURL:    "https://example.test",
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := apitest.MinimalRequest(openairesponses.API)
	request.Messages = nil
	_, err = adapter.Stream(context.Background(), request)
	if err == nil ||
		!strings.Contains(err.Error(), "validate request") ||
		!strings.Contains(err.Error(), "at least one message is required") {
		t.Fatalf("Stream() error = %v, want request validation error", err)
	}
	if requestSent {
		t.Fatal("Stream() sent an HTTP request before validation completed")
	}
}

func TestAdapterRejectsExcessiveTemperature(t *testing.T) {
	t.Parallel()

	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unexpected HTTP request")
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := apitest.MinimalRequest(openairesponses.API)
	temperature := 2.5
	request.Options.Temperature = &temperature
	_, err = adapter.Stream(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "temperature cannot exceed 2") {
		t.Fatalf("Stream() error = %v, want temperature rejection", err)
	}
}

func TestAdapterEmptyToolCallArgumentsBecomeEmptyObject(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp-1","model":"deepseek-v4-flash","status":"in_progress"}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"list","arguments":"","status":"in_progress"}}`,
		`{"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"list","arguments":"","status":"completed"}}`,
		`{"type":"response.completed","sequence_number":3,"response":{"id":"resp-1","model":"deepseek-v4-flash","status":"completed"}}`,
	})
	defer server.Close()

	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(openairesponses.API))
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
		_, _ = io.WriteString(
			w,
			"data: "+
				`{"type":"response.completed","sequence_number":0,"response":`+
				`{"id":"resp-1","model":"deepseek-v4-flash","status":"completed"}}`+
				"\n\n",
		)
	}))
	defer server.Close()

	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := apitest.MinimalRequest(openairesponses.API)
	request.Messages = append(request.Messages,
		llm.AssistantMessage{
			Role:     llm.RoleAssistant,
			API:      "anthropic-messages",
			Provider: "deepseek",
			ModelID:  "deepseek-v4-pro",
			Content: []llm.ContentPart{
				llm.NewThinkingContent("foreign plan", "opaque-signature").Part(),
				{
					Type:      llm.ContentTypeText,
					Text:      "foreign answer",
					Signature: "msg-foreign",
				},
				{
					Type: llm.ContentTypeToolCall,
					ToolCall: &llm.ToolCall{
						ID:        "call-1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path":"README.md"}`),
						Signature: "fc-foreign",
					},
				},
			},
		},
		llm.ToolResultMessage{
			Role:       llm.RoleToolResult,
			ToolCallID: "call-1",
			Content:    []llm.ContentPart{llm.NewTextContent("contents").Part()},
		},
	)

	modelStream, err := adapter.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := modelStream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	body := <-requests
	input, ok := body["input"].([]any)
	if !ok || len(input) != 5 {
		t.Fatalf("input = %#v, want five items", body["input"])
	}
	assertForeignAssistantText(t, input[1], "foreign plan")
	assertForeignAssistantText(t, input[2], "foreign answer")
	call, ok := input[3].(map[string]any)
	if !ok || call["type"] != "function_call" || call["call_id"] != "call-1" {
		t.Fatalf("function call = %#v", input[3])
	}
	if _, exists := call["id"]; exists {
		t.Errorf("foreign function call retained protocol item id: %#v", call)
	}
}

func assertForeignAssistantText(t *testing.T, value any, want string) {
	t.Helper()

	item, ok := value.(map[string]any)
	if !ok || item["role"] != "assistant" {
		t.Fatalf("assistant input = %#v", value)
	}
	if type_, exists := item["type"]; exists && type_ != "message" {
		t.Errorf("assistant input type = %#v, want message or omitted", type_)
	}
	if item["content"] != want {
		t.Errorf("assistant content = %#v, want %q", item["content"], want)
	}
	if _, exists := item["id"]; exists {
		t.Errorf("foreign assistant text retained protocol item id: %#v", item)
	}
}

func assertRequestBody(t *testing.T, body map[string]any) {
	t.Helper()

	if body["model"] != "deepseek-v4-flash" {
		t.Errorf("model = %#v", body["model"])
	}
	if body["max_output_tokens"] != float64(4_096) {
		t.Errorf("max_output_tokens = %#v", body["max_output_tokens"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %#v", body["stream"])
	}
	if body["store"] != false {
		t.Errorf("store = %#v", body["store"])
	}
	if body["instructions"] != "You are a coding agent." {
		t.Errorf("instructions = %#v", body["instructions"])
	}
	if body["temperature"] != 0.2 {
		t.Errorf("temperature = %#v", body["temperature"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Errorf("reasoning = %#v", body["reasoning"])
	}

	input, ok := body["input"].([]any)
	if !ok || len(input) != 5 {
		t.Fatalf("input = %#v, want five input items", body["input"])
	}
	if item := input[0].(map[string]any); item["role"] != "user" {
		t.Errorf("user input = %#v", item)
	}
	if item := input[1].(map[string]any); item["type"] != "reasoning" || item["id"] != "rs-prev" {
		t.Errorf("reasoning input = %#v", item)
	}
	if item := input[2].(map[string]any); item["type"] != "message" || item["id"] != "msg-prev" {
		t.Errorf("assistant text input = %#v", item)
	}
	if item := input[3].(map[string]any); item["type"] != "function_call" ||
		item["id"] != "fc-prev" ||
		item["call_id"] != "call-1" {
		t.Errorf("function call input = %#v", item)
	}
	if item := input[4].(map[string]any); item["type"] != "function_call_output" ||
		item["call_id"] != "call-1" ||
		item["output"] != "file contents" {
		t.Errorf("function output input = %#v", item)
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" ||
		tool["name"] != "read" ||
		tool["description"] != "Read a file." ||
		tool["strict"] != false {
		t.Errorf("tool = %#v", tool)
	}
}
