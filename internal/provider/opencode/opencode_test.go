package opencode_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

func modelForID(models []llm.Model, id string) (llm.Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

func TestProviderDispatchesModelsThroughConfiguredAPIs(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		if r.Header.Get("x-opencode-session") == "" {
			http.Error(w, `{"type":"MissingSessionID"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	provider, err := opencode.New(opencode.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/gateway/v1",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, modelID := range []string{
		"deepseek-v4.1-flash",
		"gpt-5.6-luna",
		"qwen3.8-max",
	} {
		model, ok := modelForID(opencode.Models(), modelID)
		if !ok {
			t.Fatalf("model %q missing from Models()", modelID)
		}
		request := llm.Request{
			Model: model,
			Messages: []llm.Message{llm.UserMessage{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
			}},
		}
		stream, err := provider.Stream(context.Background(), request)
		if err != nil {
			t.Fatalf("Stream(%q) error = %v", modelID, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close(%q) error = %v", modelID, err)
		}
	}

	got := []string{<-paths, <-paths, <-paths}
	want := []string{
		"/gateway/v1/chat/completions",
		"/gateway/v1/responses",
		"/gateway/v1/messages",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request paths = %v, want %v", got, want)
	}
}
