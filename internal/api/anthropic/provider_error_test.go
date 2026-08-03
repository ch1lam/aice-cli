package anthropic

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestNormalizeProviderErrorPreservesAnthropicMetadata(t *testing.T) {
	t.Parallel()

	requestURL, err := url.Parse("https://example.test/v1/messages")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	sdkErr := &anthropicsdk.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    &http.Request{Method: http.MethodPost, URL: requestURL},
		Response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After-Ms": []string{"2500"}},
		},
	}

	normalized := normalizeProviderError(sdkErr)
	var providerErr *llm.ProviderError
	if !errors.As(normalized, &providerErr) {
		t.Fatalf("normalizeProviderError() = %T, want *llm.ProviderError", normalized)
	}
	if providerErr.StatusCode != http.StatusTooManyRequests ||
		providerErr.RetryAfter != 2500*time.Millisecond ||
		providerErr.Transport {
		t.Fatalf("provider error = %#v", providerErr)
	}
}
