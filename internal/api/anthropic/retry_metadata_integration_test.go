package anthropic_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterNormalizesHTTPRetryMetadataWithoutSDKRetry(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After-Ms", "1500")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(
			`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
		))
	}))
	defer server.Close()

	adapter, err := anthropic.New(anthropic.Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = adapter.Stream(t.Context(), minimalRequest())
	var providerErr *llm.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Stream() error = %v, want *llm.ProviderError", err)
	}
	if providerErr.StatusCode != http.StatusTooManyRequests ||
		providerErr.Code != "rate_limit_error" ||
		providerErr.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("provider error = %#v", providerErr)
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d, want SDK retries disabled", requests.Load())
	}
}
