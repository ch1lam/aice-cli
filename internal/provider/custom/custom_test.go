package custom_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
)

func TestModelForIDAcceptsAnyID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"llama3.1:8b", "my-local-model", "gpt-oss:20b", "qwen2.5-coder:7b"} {
		model := custom.ModelForID(id)
		if model.ID != id || model.Provider != custom.ProviderID {
			t.Errorf("ModelForID(%q) = %#v", id, model)
		}
		if model.API != "openai-completions" {
			t.Errorf("ModelForID(%q) API = %q, want openai-completions", id, model.API)
		}
		if model.ThinkingLevelMap == nil {
			t.Errorf("ModelForID(%q) thinking map is nil", id)
		}
	}
}

func TestDefaultModelIsInCatalog(t *testing.T) {
	t.Parallel()

	def := custom.DefaultModel()
	found := false
	for _, m := range custom.Models() {
		if m.ID == def.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DefaultModel %q not in Models() catalog", def.ID)
	}
}

func TestProviderAcceptsArbitraryModel(t *testing.T) {
	t.Parallel()

	// Fake OpenAI-compatible server that records request and returns minimal stream.
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		// Echo model from request body to validate passthrough.
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		gotModel = string(body[:n])
		w.Header().Set("Content-Type", "text/event-stream")
		// Minimal SSE stream: one chunk with finish_reason stop and usage.
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"model\":\"test\",\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"model\":\"test\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p, err := custom.New(custom.Config{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Use an arbitrary model ID that is not in any compiled catalog.
	model := custom.ModelForID("my-arbitrary-local-model:7b")
	stream, err := p.Stream(t.Context(), llm.Request{
		Model:    model,
		Messages: []llm.Message{mustUserMessage(t, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	// Drain stream to ensure request was made.
	for {
		_, err := stream.Next()
		if err != nil {
			break
		}
	}
	if !strings.Contains(gotModel, "my-arbitrary-local-model:7b") {
		t.Errorf("request body = %q, want model id", gotModel)
	}
}

func TestConfiguredAlwaysTrueForKeylessLocal(t *testing.T) {
	t.Parallel()

	p := &custom.Provider{}
	if !p.Configured(config.Config{}) {
		t.Error("Configured(empty) = false, want true for keyless local")
	}
	if !p.Configured(config.Config{CustomBaseURL: "http://localhost:11434/v1"}) {
		t.Error("Configured with base URL = false, want true")
	}
}

func TestNewUsesDefaultBaseURLAndDummyKey(t *testing.T) {
	t.Parallel()

	p, err := custom.New(custom.Config{})
	if err != nil {
		t.Fatalf("New(empty) error = %v", err)
	}
	// Should not error and should be usable with arbitrary model.
	_, err = p.Stream(context.Background(), llm.Request{
		Model:    custom.ModelForID("test"),
		Messages: []llm.Message{mustUserMessage(t, "hi")},
	})
	// Expect network error (no server at localhost), not configuration error.
	if err == nil || strings.Contains(err.Error(), "API key is required") {
		t.Errorf("Stream(empty config) error = %v, want network error, not auth error", err)
	}
}

func mustUserMessage(t *testing.T, text string) llm.UserMessage {
	t.Helper()
	m, err := llm.NewUserMessage(llm.NewTextContent(text).Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	return m
}
