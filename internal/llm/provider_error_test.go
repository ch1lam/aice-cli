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

func TestProviderErrorClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           *llm.ProviderError
		wantPermanent bool
		wantTransient bool
	}{
		{
			name:          "billing error",
			err:           &llm.ProviderError{Code: "billing_error"},
			wantPermanent: true,
		},
		{
			name:          "insufficient quota",
			err:           &llm.ProviderError{Code: "insufficient_quota"},
			wantPermanent: true,
		},
		{
			name:          "rate limit code",
			err:           &llm.ProviderError{Code: "rate-limit-error"},
			wantTransient: true,
		},
		{
			name:          "overloaded",
			err:           &llm.ProviderError{Code: "overloaded_error"},
			wantTransient: true,
		},
		{
			name:          "too many requests",
			err:           &llm.ProviderError{StatusCode: http.StatusTooManyRequests},
			wantTransient: true,
		},
		{
			name:          "request timeout",
			err:           &llm.ProviderError{StatusCode: http.StatusRequestTimeout},
			wantTransient: true,
		},
		{
			name:          "server error",
			err:           &llm.ProviderError{StatusCode: http.StatusBadGateway},
			wantTransient: true,
		},
		{
			name: "unauthorized",
			err:  &llm.ProviderError{StatusCode: http.StatusUnauthorized},
		},
		{
			name: "transport",
			err:  &llm.ProviderError{Transport: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.err.IsPermanent(); got != test.wantPermanent {
				t.Errorf("IsPermanent() = %v, want %v", got, test.wantPermanent)
			}
			if got := test.err.IsTransient(); got != test.wantTransient {
				t.Errorf("IsTransient() = %v, want %v", got, test.wantTransient)
			}
		})
	}
}
