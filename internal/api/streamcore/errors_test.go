package streamcore

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

type apiError struct{ status int }

func (e *apiError) Error() string { return "api error" }

func TestSDKError(t *testing.T) {
	t.Parallel()

	describe := func(e *apiError) (int, string, *http.Response) {
		return e.status, "rate_limit_error", &http.Response{Header: http.Header{"Retry-After": []string{"2"}}}
	}
	var providerErr *llm.ProviderError

	transport := SDKError(errors.New("provider failed"), describe)
	if !errors.As(transport, &providerErr) || !providerErr.Transport {
		t.Fatalf("SDKError(non-API) = %#v, want transport provider error", transport)
	}

	httpErr := SDKError(fmt.Errorf("wrapped: %w", &apiError{status: http.StatusTooManyRequests}), describe)
	if !errors.As(httpErr, &providerErr) {
		t.Fatalf("SDKError(API) = %#v, want provider error", httpErr)
	}
	if providerErr.Transport || providerErr.StatusCode != http.StatusTooManyRequests || providerErr.Code != "rate_limit_error" {
		t.Fatalf("SDKError(API) = %#v, want http 429", providerErr)
	}
}
