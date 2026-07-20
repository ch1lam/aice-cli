package deepseek_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestModels(t *testing.T) {
	t.Parallel()

	want := []llm.Model{
		{
			ID:               deepseek.ModelV4Flash,
			Name:             "DeepSeek V4 Flash",
			API:              anthropic.API,
			Provider:         deepseek.ProviderID,
			SupportsThinking: true,
			InputModalities:  []llm.InputModality{llm.InputModalityText},
			ContextWindow:    1_000_000,
			MaxTokens:        384_000,
		},
		{
			ID:               deepseek.ModelV4Pro,
			Name:             "DeepSeek V4 Pro",
			API:              anthropic.API,
			Provider:         deepseek.ProviderID,
			SupportsThinking: true,
			InputModalities:  []llm.InputModality{llm.InputModalityText},
			ContextWindow:    1_000_000,
			MaxTokens:        384_000,
		},
	}
	if got := deepseek.Models(); !reflect.DeepEqual(got, want) {
		t.Errorf("Models() = %#v, want %#v", got, want)
	}
	if got := deepseek.DefaultModel(); !reflect.DeepEqual(got, want[0]) {
		t.Errorf("DefaultModel() = %#v, want %#v", got, want[0])
	}
}

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
			name: "image",
			part: llm.ContentPart{
				Type:  llm.ContentTypeImage,
				Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"},
			},
			want: "image content is not supported",
		},
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
				Messages: []llm.Message{{
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

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()

	_, err := deepseek.New(deepseek.Config{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("New() error = %v, want missing API key error", err)
	}
}
