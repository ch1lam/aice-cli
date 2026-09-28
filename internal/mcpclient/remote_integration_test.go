//go:build integration

package mcpclient

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// This opt-in check contacts DeepWiki's public, unauthenticated MCP endpoint.
// It sends only a public repository name; no local files or credentials.
// https://docs.devin.ai/work-with-devin/deepwiki-mcp
func TestDeepWikiRemoteReadOnly(t *testing.T) {
	if os.Getenv("AICE_MCP_DEEPWIKI_TEST") != "1" {
		t.Skip("set AICE_MCP_DEEPWIKI_TEST=1 to contact the public DeepWiki MCP service")
	}
	c, err := Open(t.Context(), Config{HTTP: &HTTPConfig{Endpoint: "https://mcp.deepwiki.com/mcp"}, CallTimeout: 45 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	catalog, err := c.Tools(t.Context())
	if err != nil || !catalog.Complete {
		t.Fatalf("discovery complete=%v notice=%q err=%v", catalog.Complete, catalog.Notice, err)
	}
	found := false
	for _, tool := range catalog.Items {
		if tool.Name == "read_wiki_structure" {
			found = true
		}
	}
	if !found {
		t.Fatal("public documentation structure tool not found")
	}
	result, err := c.Call(t.Context(), "read_wiki_structure", json.RawMessage(`{"repoName":"facebook/react"}`))
	if err != nil || result.State != llm.ExecutionReturned || result.IsError || len(result.Content) == 0 {
		t.Fatalf("read outcome=%s isError=%v blocks=%d err=%v", result.State, result.IsError, len(result.Content), err)
	}
	t.Logf("server=%q version=%q protocol=%q catalog=%d result_blocks=%d", c.Info().Name, c.Info().Version, c.Info().ProtocolVersion, len(catalog.Items), len(result.Content))
}
