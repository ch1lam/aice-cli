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

func TestStreamEventError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      string
		status    int
		code      string
		retryable bool
	}{
		{"server error type", `{"error":{"message":"m","type":"server_error","code":null}}`, 0, "server_error", true},
		{"rate limit code", `{"error":{"type":"requests","code":"rate_limit_exceeded"}}`, 0, "rate_limit_exceeded", true},
		{"invalid request", `{"error":{"type":"invalid_request_error","code":"context_length_exceeded"}}`, 0, "context_length_exceeded", false},
		{"insufficient quota", `{"error":{"type":"insufficient_quota","code":"insufficient_quota"}}`, 0, "insufficient_quota", false},
		{"numeric status", `{"error":{"message":"upstream","code":502}}`, http.StatusBadGateway, "", true},
		{"numeric business code", `{"error":{"message":"balance","code":1113}}`, 0, "", false},
		{"string error", `{"error":"failed"}`, 0, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("received error while streaming")
			var providerErr *llm.ProviderError
			if !errors.As(StreamEventError(cause, []byte(test.data)), &providerErr) {
				t.Fatal("want provider error")
			}
			if providerErr.Transport || providerErr.StatusCode != test.status || providerErr.Code != test.code ||
				!errors.Is(providerErr, cause) {
				t.Fatalf("StreamEventError() = %#v", providerErr)
			}
			if got := providerErr.IsTransient() && !providerErr.IsPermanent(); got != test.retryable {
				t.Fatalf("retryable = %t, want %t", got, test.retryable)
			}
		})
	}
}
