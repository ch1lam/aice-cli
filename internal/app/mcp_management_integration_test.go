//go:build integration

package app

import (
	"os"
	"testing"
)

// This opt-in contacts only the public DeepWiki MCP endpoint through the
// production management command. It never loads real credentials or executes
// a remote tool; provider and installer access remain forbidden by the helper.
func TestMCPManagementDeepWikiDiscovery(t *testing.T) {
	if os.Getenv("AICE_MCP_DEEPWIKI_TEST") != "1" {
		t.Skip("set AICE_MCP_DEEPWIKI_TEST=1 for public remote discovery")
	}
	paths := trustTestPaths(t)
	definition := `{"transport":"http","url":"https://mcp.deepwiki.com/mcp","connect_timeout":"15s","call_timeout":"20s"}`
	if result, err := managementCommand(t, paths, definition, "add", "deepwiki"); err != nil || !result.Committed {
		t.Fatalf("save public configuration: %v", err)
	}
	status, err := managementCommand(t, paths, "", "status", "user:deepwiki")
	if err != nil || len(status.Services) != 1 {
		t.Fatalf("review configured identity: %v", err)
	}
	if _, err := managementCommand(t, paths, "", "approve", "user:deepwiki", "--fingerprint", status.Services[0].Fingerprint); err != nil {
		t.Fatalf("approve exact public identity: %v", err)
	}
	result, err := managementCommand(t, paths, "", "connect", "user:deepwiki")
	if err != nil || len(result.Services) != 1 {
		t.Fatalf("public MCP discovery: %v", err)
	}
	service := result.Services[0]
	if service.State != "ready" || !service.CatalogKnown || service.ToolCount < 1 || service.EligibleTools != service.ToolCount {
		t.Fatalf("incomplete public catalog: state=%s known=%t tools=%d eligible=%d", service.State, service.CatalogKnown, service.ToolCount, service.EligibleTools)
	}
	t.Logf("production CLI configuration/approval/discovery: %d public tools, no remote tool calls", service.ToolCount)
}
