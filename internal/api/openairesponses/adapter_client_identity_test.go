package openairesponses_test

import (
	"net/http"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterSendsVersionedClientIdentity(t *testing.T) {
	t.Parallel()
	apitest.CheckClientIdentity(t, openairesponses.API, func(baseURL string, client *http.Client) (llm.Streamer, error) {
		return openairesponses.New(openairesponses.Config{
			APIKey: "test-key", BaseURL: baseURL, HTTPClient: client,
			Headers: http.Header{"User-Agent": {"generic-sdk/1.0"}},
		})
	})
}
