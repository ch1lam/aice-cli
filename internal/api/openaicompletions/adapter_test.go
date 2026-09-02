package openaicompletions_test

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

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func TestAdapterStreamsTextToolCallUsageAndDone(t *testing.T) {
	t.Parallel()

	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("request path = %q, want %q", r.URL.Path, "/chat/completions")
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
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}],"usage":null}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}],"usage":null}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}],"usage":null}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-2","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}],"usage":null}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}],"usage":null}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"README.md\"}"}}]},"finish_reason":"tool_calls"}],"usage":null}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[],"usage":{"prompt_tokens":15,"completion_tokens":7,"total_tokens":22,"completion_tokens_details":{"reasoning_tokens":2},"prompt_tokens_details":{"cached_tokens":3,"cache_write_tokens":2}}}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
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
			ID:        "kimi-k2.6",
			API:       openaicompletions.API,
			Provider:  "opencode-go",
			MaxTokens: 4_096,
			Pricing: llm.Pricing{
				Input:     0.95,
				Output:    4,
				CacheRead: 0.16,
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
				API:      openaicompletions.API,
				Provider: "opencode-go",
				ModelID:  "kimi-k2.6",
				Content: []llm.ContentPart{
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
		llm.EventTypeTextStart,
		llm.EventTypeTextDelta,
		llm.EventTypeTextDelta,
		llm.EventTypeToolCallStart,
		llm.EventTypeToolCallDelta,
		llm.EventTypeToolCallDelta,
		llm.EventTypeTextEnd,
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

	text := events[7].Content
	if text == nil || text.Text != "Hello world" {
		t.Errorf("text end content = %#v", text)
	}
	toolCall := events[8].ToolCall
	if toolCall == nil ||
		toolCall.ID != "call-2" ||
		toolCall.Name != "read" ||
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
	if events[9].Usage == nil || !reflect.DeepEqual(*events[9].Usage, wantUsage) {
		t.Errorf("usage event = %#v, want %#v", events[9].Usage, wantUsage)
	}

	done := events[10]
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
	if done.Message.API != openaicompletions.API ||
		done.Message.Provider != "opencode-go" ||
		done.Message.ModelID != "kimi-k2.6" ||
		done.Message.ResponseModelID != "kimi-k2.6-202608" ||
		done.Message.ResponseID != "chatcmpl-1" ||
		done.Message.Timestamp == 0 {
		t.Errorf("done message metadata = %#v", done.Message)
	}
	if len(done.Message.Content) != 2 {
		t.Fatalf("done message content = %#v, want two parts", done.Message.Content)
	}

	body := <-requests
	assertRequestBody(t, body)
}

func TestAdapterStreamsReasoningContentThinking(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"reasoning_content":" carefully"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":"Answer"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
		`[DONE]`,
	})
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(context.Background(), llm.Request{
		Model: llm.Model{
			ID:        "deepseek-v4-flash",
			API:       openaicompletions.API,
			Provider:  "opencode-go",
			MaxTokens: 4_096,
		},
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("Hi").Part()},
			},
		},
	})
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
		llm.EventTypeThinkingDelta,
		llm.EventTypeTextStart,
		llm.EventTypeTextDelta,
		llm.EventTypeThinkingEnd,
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

	thinking := events[6].Content
	if thinking == nil || thinking.Text != "think carefully" {
		t.Errorf("thinking end content = %#v", thinking)
	}
	text := events[7].Content
	if text == nil || text.Text != "Answer" {
		t.Errorf("text end content = %#v", text)
	}

	done := events[9]
	if done.Message == nil || done.StopReason != llm.StopReasonStop {
		t.Errorf("done = %#v, want stop", done)
	}
	if len(done.Message.Content) != 2 {
		t.Errorf("done message content = %#v, want thinking and text", done.Message.Content)
	}
}

func TestAdapterPreservesPartialThinkingOnMissingFinishReason(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"id":"chatcmpl-truncated","object":"chat.completion.chunk","model":"glm-5.3-flash","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-truncated","object":"chat.completion.chunk","model":"glm-5.3-flash","choices":[{"index":0,"delta":{"reasoning_content":"partial reasoning"},"finish_reason":null}],"usage":null}`,
		`[DONE]`,
	})
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(
		context.Background(),
		apitest.MinimalRequest(openaicompletions.API),
	)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	events := apitest.CollectEvents(t, modelStream)
	terminal := events[len(events)-1]
	if terminal.Type != llm.EventTypeError || terminal.Message == nil {
		t.Fatalf("terminal event = %#v, want error with partial message", terminal)
	}
	if !errors.Is(terminal.Err, io.ErrUnexpectedEOF) {
		t.Errorf("terminal error = %v, want io.ErrUnexpectedEOF", terminal.Err)
	}
	want := []llm.ContentPart{{
		Type: llm.ContentTypeThinking,
		Text: "partial reasoning",
	}}
	if !reflect.DeepEqual(terminal.Message.Content, want) {
		t.Errorf("partial content = %#v, want %#v", terminal.Message.Content, want)
	}
}

func TestAdapterRejectsIncompleteToolCall(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"id":"chatcmpl-3","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-3","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-3","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{"}}]},"finish_reason":"length"}],"usage":null}`,
		`[DONE]`,
	})
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(context.Background(), llm.Request{
		Model: llm.Model{
			ID:        "kimi-k2.6",
			API:       openaicompletions.API,
			Provider:  "opencode-go",
			MaxTokens: 4_096,
		},
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("Read the file.").Part()},
			},
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	events := apitest.CollectEvents(t, modelStream)
	last := events[len(events)-1]
	if last.Type != llm.EventTypeError {
		t.Fatalf("last event type = %q, want error", last.Type)
	}
	if last.Err == nil {
		t.Fatal("error event has nil Err")
	}
	if last.StopReason != llm.StopReasonError {
		t.Errorf("error stop reason = %q, want %q", last.StopReason, llm.StopReasonError)
	}
}

func TestAdapterEmptyToolCallArgumentsBecomeEmptyObject(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"id":"chatcmpl-4","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-4","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"list"}}]},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-4","object":"chat.completion.chunk","model":"kimi-k2.6-202608","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":null}`,
		`[DONE]`,
	})
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(context.Background(), llm.Request{
		Model: llm.Model{
			ID:        "kimi-k2.6",
			API:       openaicompletions.API,
			Provider:  "opencode-go",
			MaxTokens: 4_096,
		},
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("List the files.").Part()},
			},
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	events := apitest.CollectEvents(t, modelStream)
	last := events[len(events)-1]
	if last.Type != llm.EventTypeDone {
		t.Fatalf("last event type = %q, want done", last.Type)
	}
	if last.Message == nil || len(last.Message.Content) != 1 {
		t.Fatalf("done message content = %#v, want one tool call", last.Message)
	}
	part := last.Message.Content[0]
	if part.Type != llm.ContentTypeToolCall || part.ToolCall == nil {
		t.Fatalf("done content = %#v, want tool call", part)
	}
	if string(part.ToolCall.Arguments) != "{}" {
		t.Errorf("tool call arguments = %q, want {}", part.ToolCall.Arguments)
	}
}

func TestAdapterSendsReasoningEffort(t *testing.T) {
	t.Parallel()

	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(context.Background(), llm.Request{
		Model: llm.Model{
			ID:        "deepseek-v4-flash",
			API:       openaicompletions.API,
			Provider:  "opencode-go",
			MaxTokens: 4_096,
		},
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("Hi").Part()},
			},
		},
		Options: llm.StreamOptions{
			Thinking: llm.ThinkingLevelHigh,
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	body := <-requests
	if got := body["reasoning_effort"]; got != "high" {
		t.Errorf("reasoning_effort = %#v, want high", got)
	}
}

func TestAdapterOmitsDefaultMaxTokensForOpenCodeGo(t *testing.T) {
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
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	model, ok := opencodeModelForTest("deepseek-v4-flash")
	if !ok {
		t.Fatal("opencode-go deepseek-v4-flash missing from catalog")
	}
	if !model.OmitMaxTokensByDefault {
		t.Fatal("OpenCode Go model does not omit default max tokens")
	}
	modelStream, err := adapter.Stream(context.Background(), llm.Request{
		Model: model,
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("Hi").Part()},
		}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	body := <-requests
	if _, present := body["max_tokens"]; present {
		t.Errorf("max_tokens = %#v, want field omitted", body["max_tokens"])
	}
}

func TestAdapterSendsDefaultMaxTokensForOtherProviders(t *testing.T) {
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
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(context.Background(), llm.Request{
		Model: llm.Model{
			ID:        "custom-model",
			API:       openaicompletions.API,
			Provider:  "custom",
			MaxTokens: 4_096,
			Pricing:   llm.Pricing{},
		},
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("Hi").Part()},
		}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() {
		if err := modelStream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	body := <-requests
	if got := body["max_tokens"]; got != float64(4_096) {
		t.Errorf("max_tokens = %#v, want %d", got, 4_096)
	}
}

func TestAdapterThinkingWireControls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		modelID        string
		level          llm.ThinkingLevel
		thinkingFormat llm.ThinkingFormat
		wantBody       map[string]any
		wantErr        string
	}{
		{
			name:    "opencode deepseek low effort",
			modelID: "deepseek-v4-flash",
			level:   llm.ThinkingLevelLow,
			wantBody: map[string]any{
				"thinking":         map[string]any{"type": "enabled"},
				"reasoning_effort": "low",
			},
		},
		{
			name:    "opencode deepseek rejects unsupported medium",
			modelID: "deepseek-v4-flash",
			level:   llm.ThinkingLevelMedium,
			wantErr: `model "deepseek-v4-flash" does not support thinking level "medium"`,
		},
		{
			name:    "opencode deepseek high effort",
			modelID: "deepseek-v4-flash",
			level:   llm.ThinkingLevelHigh,
			wantBody: map[string]any{
				"thinking":         map[string]any{"type": "enabled"},
				"reasoning_effort": "high",
			},
		},
		{
			name:    "opencode deepseek rejects unsupported xhigh",
			modelID: "deepseek-v4-pro",
			level:   llm.ThinkingLevelXHigh,
			wantErr: `model "deepseek-v4-pro" does not support thinking level "xhigh"`,
		},
		{
			name:    "opencode deepseek max effort",
			modelID: "deepseek-v4-pro",
			level:   llm.ThinkingLevelMax,
			wantBody: map[string]any{
				"thinking":         map[string]any{"type": "enabled"},
				"reasoning_effort": "max",
			},
		},
		{
			name:    "opencode deepseek disabled",
			modelID: "deepseek-v4-flash-vision-exp",
			level:   llm.ThinkingLevelOff,
			wantBody: map[string]any{
				"thinking": map[string]any{"type": "disabled"},
			},
		},
		{
			name:    "kimi toggle without effort",
			modelID: "kimi-k2.6",
			level:   llm.ThinkingLevelHigh,
			wantBody: map[string]any{
				"thinking": map[string]any{"type": "enabled"},
			},
		},
		{
			name:    "mapped off sends provider token",
			modelID: "hy3",
			level:   llm.ThinkingLevelOff,
			wantBody: map[string]any{
				"reasoning_effort": "none",
			},
		},
		{
			name:     "standard off omits reasoning effort",
			modelID:  "glm-5.1",
			level:    llm.ThinkingLevelOff,
			wantBody: map[string]any{},
		},
		{
			name:    "toggle-only model rejects unsupported level",
			modelID: "kimi-k2.6",
			level:   llm.ThinkingLevelMedium,
			wantErr: `model "kimi-k2.6" does not support thinking level "medium"`,
		},
		{
			name:    "standard model rejects unsupported level",
			modelID: "glm-5.2",
			level:   llm.ThinkingLevelMedium,
			wantErr: `model "glm-5.2" does not support thinking level "medium"`,
		},
		{
			name:           "unknown thinking format rejected",
			modelID:        "glm-5.1",
			level:          llm.ThinkingLevelHigh,
			thinkingFormat: llm.ThinkingFormat("future-format"),
			wantErr:        `model "glm-5.1" uses unsupported thinking format "future-format"`,
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
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()

			adapter, err := openaicompletions.New(openaicompletions.Config{
				APIKey:     "test-key",
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			model, ok := opencodeModelForTest(tt.modelID)
			if !ok {
				t.Fatalf("opencode-go model %q missing from catalog", tt.modelID)
			}
			if tt.thinkingFormat != "" {
				model.ThinkingFormat = tt.thinkingFormat
			}
			modelStream, err := adapter.Stream(context.Background(), llm.Request{
				Model: model,
				Messages: []llm.Message{llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{llm.NewTextContent("Hi").Part()},
				}},
				Options: llm.StreamOptions{Thinking: tt.level},
			})
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
			for key, want := range tt.wantBody {
				if got := body[key]; !reflect.DeepEqual(got, want) {
					t.Errorf("body[%q] = %#v, want %#v", key, got, want)
				}
			}
			for _, absent := range []string{
				"thinking",
				"reasoning_effort",
				"enable_thinking",
			} {
				if _, present := tt.wantBody[absent]; present {
					continue
				}
				if _, present := body[absent]; present {
					t.Errorf("body unexpectedly contains %q: %#v", absent, body[absent])
				}
			}
		})
	}
}

func opencodeModelForTest(id string) (llm.Model, bool) {
	for _, model := range opencode.Models() {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

func TestAdapterNormalizesHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error","code":"429"}}`)
	}))
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = adapter.Stream(context.Background(), llm.Request{
		Model: llm.Model{
			ID:        "kimi-k2.6",
			API:       openaicompletions.API,
			Provider:  "opencode-go",
			MaxTokens: 4_096,
		},
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("Hi").Part()},
			},
		},
	})
	if err == nil {
		t.Fatal("Stream() error = nil, want provider error")
	}
	var providerErr *llm.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Stream() error = %v, want *llm.ProviderError", err)
	}
	if providerErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status code = %d, want %d", providerErr.StatusCode, http.StatusTooManyRequests)
	}
}

func TestAdapterRejectsExcessiveTemperature(t *testing.T) {
	t.Parallel()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:  "test-key",
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unexpected HTTP request")
		})},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := apitest.MinimalRequest(openaicompletions.API)
	temperature := 2.5
	request.Options.Temperature = &temperature
	_, err = adapter.Stream(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "temperature cannot exceed 2") {
		t.Fatalf("Stream() error = %v, want temperature rejection", err)
	}
}

func TestAdapterUnknownFinishReasonMapsToUnknownStop(t *testing.T) {
	t.Parallel()

	server := apitest.NewSSEServer(t, []string{
		`{"id":"chatcmpl-5","object":"chat.completion.chunk","model":"kimi-k2.6","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":null}`,
		`{"id":"chatcmpl-5","object":"chat.completion.chunk","model":"kimi-k2.6","choices":[{"index":0,"delta":{},"finish_reason":"bogus"}],"usage":null}`,
		`[DONE]`,
	})
	defer server.Close()

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	modelStream, err := adapter.Stream(context.Background(), apitest.MinimalRequest(openaicompletions.API))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer modelStream.Close()

	events := apitest.CollectEvents(t, modelStream)
	last := events[len(events)-1]
	if last.Type != llm.EventTypeDone || last.StopReason != llm.StopReasonUnknown {
		t.Errorf("last event = %#v, want done with unknown stop reason", last)
	}
}

func assertRequestBody(t *testing.T, body map[string]any) {
	t.Helper()

	if body["model"] != "kimi-k2.6" {
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
	if streamOptions, ok := body["stream_options"].(map[string]any); !ok ||
		streamOptions["include_usage"] != true {
		t.Errorf("stream_options = %#v", body["stream_options"])
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 4 {
		t.Fatalf("messages = %#v, want four messages", body["messages"])
	}
	if item := messages[0].(map[string]any); item["role"] != "system" ||
		item["content"] != "You are a coding agent." {
		t.Errorf("system message = %#v", item)
	}
	if item := messages[1].(map[string]any); item["role"] != "user" ||
		item["content"] != "Read the file." {
		t.Errorf("user message = %#v", item)
	}
	assistant := messages[2].(map[string]any)
	if assistant["role"] != "assistant" ||
		assistant["content"] != "I will read it." {
		t.Errorf("assistant message = %#v", assistant)
	}
	toolCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("assistant tool_calls = %#v", assistant["tool_calls"])
	}
	toolCall := toolCalls[0].(map[string]any)
	function := toolCall["function"].(map[string]any)
	if toolCall["id"] != "call-1" ||
		function["name"] != "read" ||
		function["arguments"] != `{"path":"AGENTS.md"}` {
		t.Errorf("assistant tool call = %#v", toolCall)
	}
	if item := messages[3].(map[string]any); item["role"] != "tool" ||
		item["tool_call_id"] != "call-1" ||
		item["content"] != "file contents" {
		t.Errorf("tool message = %#v", item)
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool = %#v", tool)
	}
	functionDef := tool["function"].(map[string]any)
	if functionDef["name"] != "read" ||
		functionDef["description"] != "Read a file." {
		t.Errorf("tool function = %#v", functionDef)
	}
	if _, ok := functionDef["parameters"].(map[string]any); !ok {
		t.Errorf("tool parameters = %#v", functionDef["parameters"])
	}
}
