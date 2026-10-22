package anthropic_test

import (
	"net/http"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterSendsVersionedClientIdentity(t *testing.T) {
	t.Parallel()
	apitest.CheckClientIdentity(t, anthropic.API, func(baseURL string, client *http.Client) (llm.Streamer, error) {
		return anthropic.New(anthropic.Config{
			APIKey: "test-key", BaseURL: baseURL, HTTPClient: client,
		})
	})
}
