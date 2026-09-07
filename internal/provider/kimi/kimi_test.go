package kimi_test

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

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/kimi"
)

func TestModels(t *testing.T) {
	t.Parallel()
	models := kimi.Models()
	wantIDs := []string{"kimi-for-coding", "kimi-for-coding-highspeed", "k3-256k", "k3"}
	if len(models) != len(wantIDs) {
		t.Fatalf("models = %v", models)
	}
	for i, model := range models {
		if model.ID != wantIDs[i] || model.API != openairesponses.API || model.Provider != kimi.ProviderID {
			t.Errorf("model = %#v", model)
		}
		if model.ContextWindow != 262144 || model.MaxTokens != 32768 {
			t.Errorf("budgets = %#v", model)
		}
		if got := llm.ClampThinkingLevel(model, llm.ThinkingLevelMedium); got != llm.ThinkingLevelHigh {
			t.Errorf("default thinking = %s", got)
		}
		_, supportsOff := model.ThinkingLevelMap.WireValue(llm.ThinkingLevelOff)
		if supportsOff {
			t.Error("thinking must stay enabled")
		}
		_, supportsMax := model.ThinkingLevelMap.WireValue(llm.ThinkingLevelMax)
		if supportsMax != strings.HasPrefix(model.ID, "k3") {
			t.Errorf("max support for %s", model.ID)
		}
	}
	if !reflect.DeepEqual(kimi.DefaultModel(), models[0]) {
		t.Error("default must be available to all members")
	}
	models[0].ThinkingLevelMap[llm.ThinkingLevelHigh] = nil
	if _, ok := kimi.Models()[0].ThinkingLevelMap.WireValue(llm.ThinkingLevelHigh); !ok {
		t.Error("catalog mutation leaked")
	}
}

func TestProviderDispatchesThroughResponsesAPI(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing Kimi bearer key")
		}
		if r.UserAgent() != "aice" {
			t.Errorf("User-Agent = %s", r.UserAgent())
		}
		var body struct {
			Model     string `json:"model"`
			Stream    bool   `json:"stream"`
			Store     bool   `json:"store"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model == "" || !body.Stream || body.Store || body.Reasoning.Effort != "high" {
			t.Errorf("request = %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	modelProvider, err := kimi.New(kimi.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/coding/v1/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, candidate := range kimi.Models() {
		t.Run(candidate.ID, func(t *testing.T) {
			request := llm.Request{
				Model:   candidate,
				Options: llm.StreamOptions{Thinking: llm.ClampThinkingLevel(candidate, llm.ThinkingLevelMedium)},
				Messages: []llm.Message{llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
				}},
			}
			stream, err := modelProvider.Stream(context.Background(), request)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if err := stream.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			if got := <-paths; got != "/coding/v1/responses" {
				t.Errorf("request path = %q, want /responses", got)
			}
		})
	}
}

func TestProviderRejectsIncompatibleRequestsBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	modelProvider, err := kimi.New(kimi.Config{
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
				Model: kimi.DefaultModel(),
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

	_, err := kimi.New(kimi.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}

func TestProviderDescriptor(t *testing.T) {
	t.Parallel()

	descriptor := &kimi.Provider{}
	if got := descriptor.ProviderID(); got != kimi.ProviderID {
		t.Errorf("ProviderID() = %q, want %q", got, kimi.ProviderID)
	}
	if got := descriptor.Label(); got != "Kimi Coding Plan" {
		t.Errorf("Label() = %q, want Kimi", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "Kimi Coding Plan") {
		t.Errorf("MenuDescription() = %q, want Kimi API", got)
	}
	if got := descriptor.DefaultModel(); !reflect.DeepEqual(got, kimi.DefaultModel()) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, kimi.DefaultModel())
	}
	if got := descriptor.Models(); !reflect.DeepEqual(got, kimi.Models()) {
		t.Errorf("Models() = %#v, want %#v", got, kimi.Models())
	}

	configuration := config.Config{}
	if descriptor.Configured(configuration) {
		t.Error("Configured() = true, want false without a key")
	}
	descriptor.ApplyAPIKey(&configuration, "test-key")
	if got := configuration.KimiAPIKey; got != "test-key" {
		t.Errorf("ApplyAPIKey() stored %q, want test-key", got)
	}
	if !descriptor.Configured(configuration) {
		t.Error("Configured() = false, want true with a key")
	}
	err := descriptor.CredentialNotConfiguredError()
	if err == nil || !strings.Contains(err.Error(), config.EnvKimiAPIKey) {
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
			`{"type":"response.completed","response":{"id":"resp-1","model":"kimi-for-coding","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		} {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
	defer server.Close()
	service, err := kimi.New(kimi.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	request := llm.Request{
		Model:    kimi.DefaultModel(),
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
	if done.Message.Provider != kimi.ProviderID || len(done.Message.Content) != 2 || done.Message.Usage.TotalTokens != 15 {
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
