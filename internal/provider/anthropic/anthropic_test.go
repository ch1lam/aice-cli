package anthropic_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/anthropic"
)

func TestProviderDispatchesThroughMessagesAPI(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	modelProvider, err := anthropic.New(anthropic.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, candidate := range anthropic.Models() {
		t.Run(candidate.ID, func(t *testing.T) {
			request := llm.Request{
				Model:   candidate,
				Options: llm.StreamOptions{Thinking: llm.ClampThinkingLevel(candidate, llm.ThinkingLevelOff)},
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

			if got := <-paths; got != "/v1/messages" {
				t.Errorf("request path = %q, want /v1/messages", got)
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

	modelProvider, err := anthropic.New(anthropic.Config{
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := llm.Request{
				Model: anthropic.DefaultModel(),
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

	_, err := anthropic.New(anthropic.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}

func TestProviderDescriptor(t *testing.T) {
	t.Parallel()

	descriptor := &anthropic.Provider{}
	if got := descriptor.ProviderID(); got != anthropic.ProviderID {
		t.Errorf("ProviderID() = %q, want %q", got, anthropic.ProviderID)
	}
	if got := descriptor.Label(); got != "Anthropic (Claude API)" {
		t.Errorf("Label() = %q, want Anthropic", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "Claude API") {
		t.Errorf("MenuDescription() = %q, want Anthropic API", got)
	}
	if got := descriptor.DefaultModel(); !reflect.DeepEqual(got, anthropic.DefaultModel()) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, anthropic.DefaultModel())
	}
	if got := descriptor.Models(); !reflect.DeepEqual(got, anthropic.Models()) {
		t.Errorf("Models() = %#v, want %#v", got, anthropic.Models())
	}

	configuration := config.Config{}
	if descriptor.Configured(configuration) {
		t.Error("Configured() = true, want false without a key")
	}
	descriptor.ApplyAPIKey(&configuration, "test-key")
	if got := configuration.AnthropicAPIKey; got != "test-key" {
		t.Errorf("ApplyAPIKey() stored %q, want test-key", got)
	}
	if !descriptor.Configured(configuration) {
		t.Error("Configured() = false, want true with a key")
	}
	err := descriptor.CredentialNotConfiguredError()
	if err == nil || !strings.Contains(err.Error(), config.EnvAnthropicAPIKey) {
		t.Errorf("CredentialNotConfiguredError() = %v, want env var mention", err)
	}

	if _, err := descriptor.New(config.Config{}); err == nil {
		t.Error("New() error = nil, want missing API key error")
	}
}
