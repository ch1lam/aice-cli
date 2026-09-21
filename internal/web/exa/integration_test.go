//go:build integration

package exa

import (
	"os"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/web"
)

// TestRealExaSearch performs one paid request against the real Exa API. It
// requires the integration build tag and an explicit opt-in in addition to
// the key so a key in the environment never triggers a charge by itself:
//
//	AICE_EXA_INTEGRATION=1 EXA_API_KEY=... go test -tags=integration ./internal/web/exa -run TestRealExaSearch -v
func TestRealExaSearch(t *testing.T) {
	if os.Getenv("AICE_EXA_INTEGRATION") != "1" {
		t.Skip("set AICE_EXA_INTEGRATION=1 to run the paid Exa integration test")
	}
	key := os.Getenv("EXA_API_KEY")
	if key == "" {
		t.Skip("EXA_API_KEY is not set")
	}
	client, err := New(Config{InstanceID: "integration", APIKey: key, Timeout: 25 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Search(t.Context(), web.SearchRequest{Query: "Go context package cancellation documentation", MaxResults: 3, AllowedDomains: []string{"go.dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Evidence.Sources) == 0 {
		t.Fatal("no sources returned")
	}
	if err := response.Evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	cost := "unknown"
	if response.Evidence.Diagnostics.ReportedCost != nil {
		cost = "reported"
	}
	t.Logf("sources=%d items=%d requestId=%q cost=%s", len(response.Evidence.Sources), len(response.Evidence.Items), response.Evidence.Diagnostics.UpstreamRequestID, cost)
}
