package deepseek_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestProviderRejectsUnsupportedDeepSeekContentBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	provider, err := deepseek.New(deepseek.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name string
		part llm.ContentPart
		want string
	}{
		{
			name: "redacted thinking",
			part: llm.ContentPart{
				Type:     llm.ContentTypeThinking,
				Text:     "opaque data",
				Redacted: true,
			},
			want: "redacted thinking is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := llm.Request{
				Model: deepseek.DefaultModel(),
				Messages: []llm.Message{llm.UserMessage{
					Role:    llm.RoleUser,
					Content: []llm.ContentPart{tt.part},
				}},
			}
			_, err := provider.Stream(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream() error = %v, want text %q", err, tt.want)
			}
		})
	}

	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}

func TestVisionModelsSendImageAndExactModelID(t *testing.T) {
	t.Parallel()
	// Only Flash (V4.1-Flash) supports native vision; V4 Pro is text-only.
	for _, modelID := range []string{deepseek.ModelV4Flash} {
		t.Run(modelID, func(t *testing.T) {
			t.Parallel()
			bodies := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				bodies <- string(body)
				if r.URL.Path != "/responses" {
					t.Errorf("path = %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer server.Close()
			service, err := deepseek.New(deepseek.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			var selected llm.Model
			for _, model := range deepseek.Models() {
				if model.ID == modelID {
					selected = model
				}
			}
			stream, err := service.Stream(t.Context(), llm.Request{
				Model:   selected,
				Options: llm.StreamOptions{Thinking: llm.ThinkingLevelHigh},
				Messages: []llm.Message{llm.UserMessage{
					Role: llm.RoleUser,
					Content: []llm.ContentPart{
						llm.NewTextContent("Describe this image").Part(),
						{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}},
					},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			body := <-bodies
			var payload struct {
				Model     string `json:"model"`
				Reasoning struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Model != modelID {
				t.Errorf("model = %q", payload.Model)
			}
			if payload.Reasoning.Effort != "high" {
				t.Errorf("effort = %q", payload.Reasoning.Effort)
			}
			if !strings.Contains(body, `"type":"input_image"`) || !strings.Contains(body, "data:image/png;base64,aW1hZ2U=") {
				t.Errorf("image missing from request: %s", body)
			}

		})
	}
}

func TestV4ProRejectsImageBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	service, err := deepseek.New(deepseek.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var selected llm.Model
	for _, model := range deepseek.Models() {
		if model.ID == deepseek.ModelV4Pro {
			selected = model
		}
	}
	_, err = service.Stream(t.Context(), llm.Request{
		Model: selected,
		Messages: []llm.Message{llm.UserMessage{
			Role: llm.RoleUser,
			Content: []llm.ContentPart{
				llm.NewTextContent("Describe this image").Part(),
				{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}},
			},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "image content is not supported") {
		t.Fatalf("Stream() error = %v, want image unsupported", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("HTTP requests = %d, want 0", got)
	}
}
