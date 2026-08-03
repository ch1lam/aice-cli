package openairesponses

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	openaisdk "github.com/openai/openai-go/v3"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestNormalizeProviderErrorPreservesOpenAIMetadata(t *testing.T) {
	t.Parallel()

	requestURL, err := url.Parse("https://example.test/responses")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	sdkErr := &openaisdk.Error{
		StatusCode: http.StatusServiceUnavailable,
		Code:       "server_error",
		Request:    &http.Request{Method: http.MethodPost, URL: requestURL},
		Response: &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Retry-After": []string{"3"}},
		},
	}

	normalized := normalizeProviderError(sdkErr)
	var providerErr *llm.ProviderError
	if !errors.As(normalized, &providerErr) {
		t.Fatalf("normalizeProviderError() = %T, want *llm.ProviderError", normalized)
	}
	if providerErr.StatusCode != http.StatusServiceUnavailable ||
		providerErr.Code != "server_error" ||
		providerErr.RetryAfter != 3*time.Second ||
		providerErr.Transport {
		t.Fatalf("provider error = %#v", providerErr)
	}
}
