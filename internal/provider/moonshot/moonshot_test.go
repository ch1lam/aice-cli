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
		t.Errorf("Label() = %q, want Moonshot API", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "Moonshot API") {
		t.Errorf("MenuDescription() = %q, want Moonshot API", got)
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
	for _, model := range moonshot.Models() {
		t.Run(model.ID, func(t *testing.T) {
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
				Model:    model,
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
		})
	}
}

func TestCatalogResponsesAndThinking(t *testing.T) {
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
						if r.URL.String() != "https://api.moonshot.cn/v1/responses" {
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
						wantEffort := string(level)
						if level == llm.ThinkingLevelOff {
							wantEffort = "none"
						}
						reasoning, ok := body["reasoning"].(map[string]any)
						if !ok || reasoning["effort"] != wantEffort {
							t.Errorf("reasoning = %#v, want effort %q", body["reasoning"], wantEffort)
						}
						wantContext, wantOutput := int64(262144), float64(32768)
						if model.ID == "kimi-k3" {
							wantContext, wantOutput = 1048576, 131072
						}
						if model.ContextWindow != wantContext || body["max_output_tokens"] != wantOutput {
							t.Error("wrong Responses budgets")
						}
						if _, ok := body["thinking"]; ok {
							t.Error("unexpected Chat Completions thinking toggle")
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
