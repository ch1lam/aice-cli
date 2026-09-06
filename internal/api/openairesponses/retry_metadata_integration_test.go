package openairesponses_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterNormalizesHTTPRetryMetadataWithoutSDKRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		code       string
		retryAfter string
		wantDelay  time.Duration
	}{
		{"rate limit", http.StatusTooManyRequests, "rate_limit_exceeded", "2", 2 * time.Second},
		{"server unavailable", http.StatusServiceUnavailable, "server_error", "3", 3 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", test.retryAfter)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(
					`{"error":{"message":"unavailable","type":"api_error","code":"` + test.code + `","param":null}}`,
				))
			}))
			defer server.Close()

			adapter, err := openairesponses.New(openairesponses.Config{
				APIKey:     "test-key",
				BaseURL:    server.URL,
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = adapter.Stream(t.Context(), apitest.MinimalRequest(openairesponses.API))
			var providerErr *llm.ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("Stream() error = %v, want *llm.ProviderError", err)
			}
			if providerErr.StatusCode != test.status ||
				providerErr.Code != test.code ||
				providerErr.RetryAfter != test.wantDelay ||
				providerErr.Transport {
				t.Fatalf("provider error = %#v", providerErr)
			}
			if requests.Load() != 1 {
				t.Fatalf("HTTP requests = %d, want SDK retries disabled", requests.Load())
			}
		})
	}
}
