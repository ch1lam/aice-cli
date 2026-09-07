package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestModels(t *testing.T) {
	t.Parallel()
	models := Models()
	var ids []string
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.ContextWindow != 272_000 {
			t.Errorf("%s default context = %d, want 272000", model.ID, model.ContextWindow)
		}
	}
	want := []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}
	if !slices.Equal(ids, want) {
		t.Fatalf("model IDs = %v, want %v", ids, want)
	}
	if DefaultModel().ID != "gpt-5.6-terra" {
		t.Fatal("catalog update changed the default model")
	}
	astra := models[0]
	if astra.ContextWindow != 272_000 || astra.MaxTokens != 128_000 ||
		!slices.Contains(astra.InputModalities, llm.InputModalityImage) {
		t.Fatalf("incorrect Astra metadata: %#v", astra)
	}
	wantLevels := []llm.ThinkingLevel{llm.ThinkingLevelLow, llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax}
	if !slices.Equal(llm.SupportedThinkingLevels(astra), wantLevels) {
		t.Fatal("incorrect Astra reasoning levels")
	}
}

func saveTestCredential(t *testing.T, paths config.Paths, expires time.Time) {
	t.Helper()
	_, err := config.UpdateCodexCredentials(t.Context(), paths, func(config.CodexCredentials) (config.CodexCredentials, error) {
		return config.CodexCredentials{AccessToken: testToken(), RefreshToken: "refresh", AccountID: "test-account", ExpiresAt: expires}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func codexRequest() llm.Request {
	return llm.Request{Model: DefaultModel(), Options: llm.StreamOptions{Thinking: llm.ThinkingLevelHigh},
		Messages: []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("read a file").Part()}}},
	}
}

func TestCodexStreamsToolsAndReplaysEncryptedReasoning(t *testing.T) {
	t.Parallel()
	paths := config.Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	saveTestCredential(t, paths, time.Now().Add(time.Hour))
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" || r.Header.Get("Authorization") != "Bearer "+testToken() || r.Header.Get("Chatgpt-Account-Id") != "test-account" || r.Header.Get("Originator") != "aice" {
			t.Error("incorrect subscription routing")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		if _, exists := body["max_output_tokens"]; exists {
			t.Error("unsupported max_output_tokens sent")
		}
		if body["store"] != false || body["stream"] != true || body["instructions"] == "" || body["parallel_tool_calls"] != true {
			t.Error("invalid Codex body")
		}
		include, _ := json.Marshal(body["include"])
		if !strings.Contains(string(include), "reasoning.encrypted_content") {
			t.Error("missing encrypted reasoning include")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp","status":"in_progress"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`,
			`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"Plan"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"Plan"}],"encrypted_content":"encrypted-state"}}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":\"README.md\"}"}`,
			`{"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"path\":\"README.md\"}"}`,
			`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"README.md\"}"}}`,
			`{"type":"response.done","response":{"id":"resp","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()
	p := &Provider{paths: paths, baseURL: server.URL + "/codex"}
	request := codexRequest()
	request.Model = Models()[0] // Exercise Astra through the subscription endpoint.
	stream, err := p.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	events := apitest.CollectEvents(t, stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	var assistant *llm.AssistantMessage
	for _, event := range events {
		if event.Type == llm.EventTypeDone {
			assistant = event.Message
		}
	}
	if assistant == nil || assistant.StopReason != llm.StopReasonToolUse || assistant.Provider != ProviderID || assistant.Usage.TotalTokens != 15 {
		t.Fatal("missing completed tool response or usage")
	}
	if !strings.Contains(assistant.Content[0].Signature, "encrypted-state") {
		t.Fatal("lost encrypted reasoning")
	}
	<-requests
	request.Messages = append(request.Messages, *assistant, llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call_1", ToolName: "read", Content: []llm.ContentPart{llm.NewTextContent("file contents").Part()}})
	stream, err = p.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	replay, _ := json.Marshal((<-requests)["input"])
	for _, expected := range []string{"encrypted-state", "function_call_output", "call_1", "file contents"} {
		if !strings.Contains(string(replay), expected) {
			t.Errorf("replay omitted %s", expected)
		}
	}
}

func TestCodexConcurrentStreamsRefreshOnceAndLogoutStopsNewRequests(t *testing.T) {
	t.Parallel()
	paths := config.Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	saveTestCredential(t, paths, time.Now().Add(-time.Hour))
	var refreshes, requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			refreshes.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": testToken(), "refresh_token": "rotated", "expires_in": 3600})
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	p := &Provider{paths: paths, auth: AuthClient{BaseURL: server.URL}, baseURL: server.URL}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			stream, err := p.Stream(t.Context(), codexRequest())
			if err != nil {
				t.Error(err)
				return
			}
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 || requests.Load() != 4 {
		t.Fatalf("refreshes/requests = %d/%d", refreshes.Load(), requests.Load())
	}
	stored, err := config.LoadCodexCredentials(paths)
	if err != nil || stored.RefreshToken != "rotated" {
		t.Fatalf("rotation not persisted: %v", err)
	}
	_, err = config.UpdateCodexCredentials(t.Context(), paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return config.CodexCredentials{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(t.Context(), codexRequest()); err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("logout error = %v", err)
	}
	if requests.Load() != 4 {
		t.Fatal("logged-out provider made another request")
	}
}

func TestCodexRejectsAPIKeyAndIncompatibleModel(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	if p.Configured(config.Config{OpenAIAPIKey: "api-key"}) {
		t.Fatal("API key enabled subscription")
	}
	if _, err := p.SaveAPIKey("api-key"); err == nil {
		t.Fatal("accepted API key")
	}
	request := codexRequest()
	request.Model.Provider = "openai"
	if _, err := p.Stream(t.Context(), request); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("error = %v", err)
	}
	for _, model := range Models() {
		if model.Provider != ProviderID || model.ContextWindow <= model.MaxTokens || len(llm.SupportedThinkingLevels(model)) != 5 {
			t.Fatal("invalid model catalog")
		}
		if model.Pricing != (llm.Pricing{}) {
			t.Fatal("subscription must not quote API billing prices")
		}
	}
}
