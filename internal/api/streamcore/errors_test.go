package streamcore

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestNormalizeError(t *testing.T) {
	t.Parallel()

	base := errors.New("provider failed")

	transport := NormalizeError(base, nil)
	var providerErr *llm.ProviderError
	if !errors.As(transport, &providerErr) || !providerErr.Transport {
		t.Fatalf("NormalizeError(nil info) = %#v, want transport provider error", transport)
	}

	httpErr := NormalizeError(base, &ErrorInfo{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limit_error",
		Header:     http.Header{"Retry-After": []string{"2"}},
	})
	if !errors.As(httpErr, &providerErr) {
		t.Fatalf("NormalizeError(http) = %#v, want provider error", httpErr)
	}
	if providerErr.Transport || providerErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("NormalizeError(http) = %#v, want http 429", providerErr)
	}
}
