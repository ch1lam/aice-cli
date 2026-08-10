package openai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
)

func TestModels(t *testing.T) {
	t.Parallel()

	thinkingLevels := []llm.ThinkingLevel{
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	}
	modalities := []llm.InputModality{
		llm.InputModalityText,
		llm.InputModalityImage,
	}
	want := []llm.Model{
		{
			ID:               openai.ModelGPT56,
			Name:             "GPT-5.6",
			API:              openairesponses.API,
			Provider:         openai.ProviderID,
			SupportsThinking: true,
			ThinkingLevels:   thinkingLevels,
			InputModalities:  modalities,
			ContextWindow:    1_050_000,
			MaxTokens:        128_000,
			Pricing: llm.Pricing{
				Input: 5, Output: 30, CacheRead: 0.5, CacheWrite: 6.25,
			},
		},
		{
			ID:               openai.ModelGPT56Terra,
			Name:             "GPT-5.6 Terra",
			API:              openairesponses.API,
			Provider:         openai.ProviderID,
			SupportsThinking: true,
			ThinkingLevels:   thinkingLevels,
			InputModalities:  modalities,
			ContextWindow:    1_050_000,
			MaxTokens:        128_000,
			Pricing: llm.Pricing{
				Input: 2, Output: 12, CacheRead: 0.2, CacheWrite: 2.5,
			},
		},
		{
			ID:               openai.ModelGPT56Luna,
			Name:             "GPT-5.6 Luna",
			API:              openairesponses.API,
			Provider:         openai.ProviderID,
			SupportsThinking: true,
			ThinkingLevels:   thinkingLevels,
			InputModalities:  modalities,
			ContextWindow:    1_050_000,
			MaxTokens:        128_000,
			Pricing: llm.Pricing{
				Input: 1, Output: 6, CacheRead: 0.1, CacheWrite: 1.25,
			},
		},
	}
	if got := openai.Models(); !reflect.DeepEqual(got, want) {
		t.Errorf("Models() = %#v, want %#v", got, want)
	}
	if got := openai.DefaultModel(); !reflect.DeepEqual(got, want[1]) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, want[1])
	}
}

func TestProviderDispatchesThroughResponsesAPI(t *testing.T) {
	t.Parallel()

	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	}))
	defer server.Close()

	modelProvider, err := openai.New(openai.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := llm.Request{
		Model: openai.DefaultModel(),
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

	if got := <-paths; got != "/responses" {
		t.Errorf("request path = %q, want /responses", got)
	}
}

func TestProviderRejectsIncompatibleRequestsBeforeHTTP(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	modelProvider, err := openai.New(openai.Config{
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
				Model: openai.DefaultModel(),
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

	_, err := openai.New(openai.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}

func TestProviderDescriptor(t *testing.T) {
	t.Parallel()

	descriptor := &openai.Provider{}
	if got := descriptor.ProviderID(); got != openai.ProviderID {
		t.Errorf("ProviderID() = %q, want %q", got, openai.ProviderID)
	}
	if got := descriptor.Label(); got != "OpenAI" {
		t.Errorf("Label() = %q, want OpenAI", got)
	}
	if got := descriptor.MenuDescription(); !strings.Contains(got, "OpenAI API") {
		t.Errorf("MenuDescription() = %q, want OpenAI API", got)
	}
	if got := descriptor.DefaultModel(); !reflect.DeepEqual(got, openai.DefaultModel()) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, openai.DefaultModel())
	}
	if got := descriptor.Models(); !reflect.DeepEqual(got, openai.Models()) {
		t.Errorf("Models() = %#v, want %#v", got, openai.Models())
	}

	configuration := config.Config{}
	if descriptor.Configured(configuration) {
		t.Error("Configured() = true, want false without a key")
	}
	descriptor.ApplyAPIKey(&configuration, "test-key")
	if got := configuration.OpenAIAPIKey; got != "test-key" {
		t.Errorf("ApplyAPIKey() stored %q, want test-key", got)
	}
	if !descriptor.Configured(configuration) {
		t.Error("Configured() = false, want true with a key")
	}
	err := descriptor.CredentialNotConfiguredError()
	if err == nil || !strings.Contains(err.Error(), config.EnvOpenAIAPIKey) {
		t.Errorf("CredentialNotConfiguredError() = %v, want env var mention", err)
	}

	if _, err := descriptor.New(config.Config{}); err == nil {
		t.Error("New() error = nil, want missing API key error")
	}
}
