package moonshot_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/moonshot"
)

func TestProviderRejectsIncompatibleRequestsBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	modelProvider, err := moonshot.New(moonshot.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*llm.Request)
		want   string
	}{
		{
			name: "provider mismatch",
			mutate: func(request *llm.Request) {
				request.Model.Provider = "other"
			},
			want: "model provider",
		},
		{
			name: "unsupported model",
			mutate: func(request *llm.Request) {
				request.Model.ID = "gpt-unknown"
			},
			want: "unsupported model",
		},
		{
			name: "API mismatch",
			mutate: func(request *llm.Request) {
				request.Model.API = "other-api"
			},
			want: "API",
		},
		{
			name: "redacted thinking",
			mutate: func(request *llm.Request) {
				request.Messages = []llm.Message{llm.UserMessage{
					Role: llm.RoleUser,
					Content: []llm.ContentPart{{
						Type:     llm.ContentTypeThinking,
						Text:     "opaque data",
						Redacted: true,
					}},
				}}
			},
			want: "redacted thinking is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := llm.Request{
				Model: moonshot.DefaultModel(),
				Messages: []llm.Message{llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}},
			}
			tt.mutate(&request)
			_, err := modelProvider.Stream(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream() error = %v, want text %q", err, tt.want)
			}
		})
	}

	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()

	_, err := moonshot.New(moonshot.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}

func TestProviderDescriptor(t *testing.T) {
	t.Parallel()

	descriptor := &moonshot.Provider{}
	if got := descriptor.ProviderID(); got != moonshot.ProviderID {
		t.Errorf("ProviderID() = %q, want %q", got, moonshot.ProviderID)
	}
	if got := descriptor.Label(); got != "Moonshot API" {
		t.Errorf("Label() = %q, want Kimi", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "Moonshot API") {
		t.Errorf("MenuDescription() = %q, want Kimi API", got)
	}
	if got := descriptor.DefaultModel(); !reflect.DeepEqual(got, moonshot.DefaultModel()) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, moonshot.DefaultModel())
	}
	if got := descriptor.Models(); !reflect.DeepEqual(got, moonshot.Models()) {
		t.Errorf("Models() = %#v, want %#v", got, moonshot.Models())
	}

	configuration := config.Config{}
	if descriptor.Configured(configuration) {
		t.Error("Configured() = true, want false without a key")
	}
	descriptor.ApplyAPIKey(&configuration, "test-key")
	if got := configuration.MoonshotAPIKey; got != "test-key" {
		t.Errorf("ApplyAPIKey() stored %q, want test-key", got)
	}
	if !descriptor.Configured(configuration) {
		t.Error("Configured() = false, want true with a key")
	}
	err := descriptor.CredentialNotConfiguredError()
	if err == nil || !strings.Contains(err.Error(), config.EnvMoonshotAPIKey) {
		t.Errorf("CredentialNotConfiguredError() = %v, want env var mention", err)
	}

	if _, err := descriptor.New(config.Config{}); err == nil {
		t.Error("New() error = nil, want missing API key error")
	}
}

func TestResponsesToolRoundTrip(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs-1","type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"Inspect the file"}],"status":"completed"}}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"read","arguments":"{\"path\":\"README.md\"}","status":"completed"}}`,
			`{"type":"response.completed","response":{"id":"resp-1","model":"kimi-k3","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		} {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
	defer server.Close()
	service, err := moonshot.New(moonshot.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	request := llm.Request{
		Model:    moonshot.DefaultModel(),
		Messages: []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("Read README.md").Part()}}},
		Tools:    []llm.ToolDefinition{{Name: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
		Options:  llm.StreamOptions{Thinking: llm.ThinkingLevelHigh},
	}
	stream, err := service.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	events := apitest.CollectEvents(t, stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	done := events[len(events)-1]
	if done.Type != llm.EventTypeDone || done.StopReason != llm.StopReasonToolUse || done.Message == nil {
		t.Fatalf("done = %#v", done)
	}
	if done.Message.Provider != moonshot.ProviderID || len(done.Message.Content) != 2 || done.Message.Usage.TotalTokens != 15 {
		t.Fatalf("message = %#v", done.Message)
	}
	request.Messages = append(request.Messages, *done.Message, llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call-1", Content: []llm.ContentPart{llm.NewTextContent("README contents").Part()}})
	stream, err = service.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	<-requests
	body := <-requests
	input := body["input"].([]any)
	reasoning := input[1].(map[string]any)
	call := input[2].(map[string]any)
	result := input[3].(map[string]any)
	if reasoning["id"] != "rs-1" || reasoning["type"] != "reasoning" {
		t.Errorf("reasoning replay = %#v", reasoning)
	}
	if call["call_id"] != "call-1" || result["call_id"] != "call-1" || result["output"] != "README contents" {
		t.Errorf("call/result = %#v/%#v", call, result)
	}
}

func TestCatalogDispatchAndThinking(t *testing.T) {
	t.Parallel()
	models := moonshot.Models()
	wantIDs := []string{"kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6"}
	if len(models) != len(wantIDs) {
		t.Fatalf("catalog = %#v", models)
	}
	for i, model := range models {
		if model.ID != wantIDs[i] {
			t.Fatalf("model = %s", model.ID)
		}
		for _, level := range llm.SupportedThinkingLevels(model) {
			t.Run(model.ID+"/"+string(level), func(t *testing.T) {
				calls := 0
				service, err := moonshot.New(moonshot.Config{
					APIKey: "platform-key",
					HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						path := "/v1/chat/completions"
						if model.ID == "kimi-k3" {
							path = "/v1/responses"
						}
						if r.URL.String() != "https://api.moonshot.cn"+path {
							t.Errorf("default endpoint = %s", r.URL)
						}
						if r.Header.Get("Authorization") != "Bearer platform-key" {
							t.Error("wrong credential")
						}
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body["model"] != model.ID || body["stream"] != true {
							t.Errorf("body = %#v", body)
						}
						switch model.ID {
						case "kimi-k3":
							if body["reasoning"].(map[string]any)["effort"] != string(level) {
								t.Error("wrong Responses effort")
							}
							if model.ContextWindow != 1048576 || body["max_output_tokens"] != float64(131072) {
								t.Error("wrong K3 budgets")
							}
						case "kimi-k2.6":
							want := "enabled"
							if level == llm.ThinkingLevelOff {
								want = "disabled"
							}
							if body["thinking"].(map[string]any)["type"] != want {
								t.Error("wrong thinking toggle")
							}
						default:
							if _, ok := body["thinking"]; ok {
								t.Error("K2.7 must omit thinking")
							}
						}
						if _, ok := body["reasoning_effort"]; ok {
							t.Error("unexpected Chat Completions effort")
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
					})},
				})
				if err != nil {
					t.Fatal(err)
				}
				request := apitest.MinimalRequest(model.API)
				request.Model = model
				request.Options.Thinking = level
				stream, err := service.Stream(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("requests = %d", calls)
				}
			})
		}
	}
	models[0].ThinkingLevelMap[llm.ThinkingLevelHigh] = nil
	if !moonshot.DefaultModel().ThinkingLevelMap.Supports(llm.ThinkingLevelHigh) {
		t.Error("catalog mutated")
	}
	descriptor := &moonshot.Provider{}
	if descriptor.Configured(config.Config{KimiAPIKey: "coding-key", OpenAIAPIKey: "openai-key"}) {
		t.Error("accepted another provider's key")
	}
}

func TestChatCompletionsPreservesThinkingForToolReplay(t *testing.T) {
	t.Parallel()
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"id":"chat-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"Inspect the file"}}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
	defer server.Close()
	descriptor := &moonshot.Provider{}
	service, err := descriptor.New(config.Config{MoonshotAPIKey: "platform-key", MoonshotBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	model := moonshot.Models()[1]
	request := apitest.MinimalRequest(model.API)
	request.Model = model
	request.Options.Thinking = llm.ThinkingLevelHigh
	stream, err := service.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	events := apitest.CollectEvents(t, stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	done := events[len(events)-1]
	if done.Message == nil || done.StopReason != llm.StopReasonToolUse || done.Message.Usage.TotalTokens != 15 {
		t.Fatalf("done = %#v", done)
	}
	request.Messages = append(request.Messages, *done.Message, llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call-1", Content: []llm.ContentPart{llm.NewTextContent("file contents").Part()}})
	stream, err = service.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	<-requests
	body := <-requests
	messages := body["messages"].([]any)
	assistant := messages[1].(map[string]any)
	result := messages[2].(map[string]any)
	if assistant["reasoning_content"] != "Inspect the file" || result["tool_call_id"] != "call-1" {
		t.Errorf("replay = %#v", messages)
	}
}
