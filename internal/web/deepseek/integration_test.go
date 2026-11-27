//go:build integration

package deepseek

import (
	"os"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/web"
)

// TestRealDeepSeekSearch performs one paid request against the real DeepSeek
// Search API. It requires the integration build tag and an explicit opt-in in
// addition to the key, so a key in the environment never triggers a charge:
//
//	AICE_DEEPSEEK_SEARCH_INTEGRATION=1 DEEPSEEK_API_KEY=... go test -tags=integration ./internal/web/deepseek -run TestRealDeepSeekSearch -v
func TestRealDeepSeekSearch(t *testing.T) {
	if os.Getenv("AICE_DEEPSEEK_SEARCH_INTEGRATION") != "1" {
		t.Skip("set AICE_DEEPSEEK_SEARCH_INTEGRATION=1 to run the paid DeepSeek search integration test")
	}
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY is not set")
	}
	client, err := New(Config{InstanceID: "integration", APIKey: key, Timeout: 25 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Search(t.Context(), web.SearchRequest{Query: "Go context package cancellation", MaxResults: 3, AllowedDomains: []string{"go.dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Evidence.Sources) == 0 {
		t.Fatal("no sources returned")
	}
	if err := response.Evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Logf("sources=%d items=%d first=%s", len(response.Evidence.Sources), len(response.Evidence.Items), response.Evidence.Sources[0].URL)
}
