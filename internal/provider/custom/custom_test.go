package custom_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
		if !slices.Contains(model.InputModalities, llm.InputModalityImage) {
			t.Errorf("ModelForID(%q) does not allow image input", id)
		}
	}
}

func TestProviderPassesImagesToEndpoint(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var body []byte
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var err error
				body, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if status != http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"image input unsupported","type":"invalid_request_error"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			p, err := custom.New(custom.Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			model := custom.ModelForID("unknown-vision-model")
			image := llm.ContentPart{Type: llm.ContentTypeImage, Image: &llm.ImageContent{
				MIMEType: "image/png", Data: []byte("image-payload"),
			}}
			user, err := llm.NewUserMessage(image)
			if err != nil {
				t.Fatal(err)
			}
			assistant := llm.NewAssistantMessage(model)
			assistant.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{
				ID: "read-image", Name: "read", Arguments: json.RawMessage(`{"path":"photo.png"}`),
			}}}
			assistant.StopReason = llm.StopReasonToolUse
			result, err := llm.NewToolResultMessage(llm.ToolResult{
				CallID: "read-image", Name: "read", Content: []llm.ContentPart{image},
			})
			if err != nil {
				t.Fatal(err)
			}
			stream, err := p.Stream(t.Context(), llm.Request{
				Model: model, Messages: []llm.Message{user, assistant, result},
			})
			if err == nil {
				defer stream.Close()
				_, err = stream.Next()
			}
			if status == http.StatusOK {
				if err != nil && err != io.EOF {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "image input unsupported") {
				t.Fatalf("error = %v, want endpoint rejection", err)
			}
			payload := base64.StdEncoding.EncodeToString(image.Image.Data)
			if strings.Count(string(body), "data:image/png;base64,"+payload) != 2 {
				t.Fatalf("user and tool images did not both reach endpoint: %s", body)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want one without fallback", requests)
			}
		})
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
