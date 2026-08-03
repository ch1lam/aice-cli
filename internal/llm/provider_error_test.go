package llm_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestNewHTTPProviderErrorPreservesRetryMetadata(t *testing.T) {
	t.Parallel()

	source := errors.New("rate limited")
	err := llm.NewHTTPProviderError(source, http.StatusTooManyRequests, "rate_limit_error", http.Header{
		"Retry-After":    []string{"10"},
		"Retry-After-Ms": []string{"1250"},
	})

	if err.StatusCode != http.StatusTooManyRequests ||
		err.Code != "rate_limit_error" ||
		err.RetryAfter != 1250*time.Millisecond ||
		err.Transport {
		t.Fatalf("NewHTTPProviderError() = %#v", err)
	}
	if !errors.Is(err, source) {
		t.Fatalf("errors.Is() = false, want source error")
	}
}

func TestNewTransportProviderErrorPreservesCause(t *testing.T) {
	t.Parallel()

	source := errors.New("connection reset")
	err := llm.NewTransportProviderError(source)
	if !err.Transport || !errors.Is(err, source) {
		t.Fatalf("NewTransportProviderError() = %#v", err)
	}
}
