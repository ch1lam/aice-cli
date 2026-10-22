package openaicompletions_test

import (
	"net/http"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestAdapterSendsVersionedClientIdentity(t *testing.T) {
	t.Parallel()
	apitest.CheckClientIdentity(t, openaicompletions.API, func(baseURL string, client *http.Client) (llm.Streamer, error) {
		return openaicompletions.New(openaicompletions.Config{
			APIKey: "test-key", BaseURL: baseURL, HTTPClient: client,
		})
	})
}
