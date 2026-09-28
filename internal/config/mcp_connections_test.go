package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestMCPConnectionApprovalSourceIdentityAndPersistence(t *testing.T) {
	paths := testPaths(t.TempDir())
	settings := config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": httpMCP()}}
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": settings})
	writeJSON(t, paths.GlobalAuth, map[string]any{"openai_api_key": "provider-secret"})
	c := loadMCP(t, paths, config.LoadOptions{})
	server := c.MCP.Servers["user:docs"]
	if c.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
		t.Fatal("configuration authorized itself")
	}
	commit, err := config.SaveMCPConnectionDecision(t.Context(), paths, server, config.MCPConnectionAllow)
	if err != nil || !commit.Committed {
		t.Fatalf("save: %v %v", commit, err)
	}
	approved := loadMCP(t, paths, config.LoadOptions{})
	if approved.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAllow || approved.OpenAIAPIKey != "provider-secret" {
		t.Fatal("approval not isolated or not persisted")
	}
	next, err := c.WithMCPConnectionDecision(server.Key, server.Fingerprint, config.MCPConnectionAllow)
	if err != nil || next.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAllow || c.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
		t.Fatal("snapshot approval aliases old state")
	}
	if _, err := next.WithMCPConnectionDecision(server.Key, "stale", config.MCPConnectionDeny); err == nil {
		t.Fatal("stale identity accepted")
	}
	rotated, err := next.WithMCPCredential(server.Key, server.CredentialScope, "token", "account-secret")
	if err != nil || rotated.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
		t.Fatal("credential change retained approval")
	}
	changed := settings.Servers["docs"]
	changed.URL = "https://other.example/mcp"
	settings.Servers["docs"] = changed
	moved, err := next.WithMCP(settings)
	if err != nil || moved.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
		t.Fatal("endpoint change retained approval")
	}
	if _, err := config.SaveMCPConnectionDecision(t.Context(), paths, moved.MCP.Servers[server.Key], config.MCPConnectionDeny); err != nil {
		t.Fatal(err)
	}
	// One decision per service, so a new binding replaces, not accumulates, grants.
	reloaded := loadMCP(t, paths, config.LoadOptions{})
	if reloaded.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
		t.Fatal("changed binding granted original configuration")
	}
	if _, err := config.SaveMCPConnectionDecision(t.Context(), paths, server, config.MCPConnectionAsk); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(paths.GlobalAuth)
	if strings.Contains(string(data), server.Fingerprint) || !strings.Contains(string(data), "provider-secret") {
		t.Fatal("forget did not preserve unrelated credentials")
	}
}

func TestMCPConnectionApprovalCannotComeFromSettings(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	settings := config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": {Transport: "http", URL: "https://example.com/mcp"}}}
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": settings})
	writeJSON(t, paths.ProjectSettings, map[string]any{"mcp": settings})
	c := loadMCP(t, paths, config.LoadOptions{})
	forged := map[string]any{}
	for key, server := range c.MCP.Servers {
		forged[key] = map[string]any{"fingerprint": server.Fingerprint, "decision": "allow"}
	}
	writeJSON(t, paths.ProjectSettings, map[string]any{"mcp": settings, "mcp_connections": forged})
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": settings, "mcp_connections": forged})
	c = loadMCP(t, paths, config.LoadOptions{})
	if len(c.Diagnostics) != 2 {
		t.Fatalf("missing ignored-source diagnostics: %v", c.Diagnostics)
	}
	for key := range c.MCP.Servers {
		if c.MCP.ConnectionDecision(key) != config.MCPConnectionAsk {
			t.Fatal("settings self-authorized")
		}
	}
	for key, server := range c.MCP.Servers {
		if server.Source.Kind != "project" {
			continue
		}
		if _, err := config.SaveMCPConnectionDecision(t.Context(), paths, server, config.MCPConnectionAllow); err != nil {
			t.Fatal(err)
		}
		reloaded := loadMCP(t, paths, config.LoadOptions{})
		if reloaded.MCP.ConnectionDecision(key) != config.MCPConnectionAllow || reloaded.MCP.ConnectionDecision("user:docs") != config.MCPConnectionAsk {
			t.Fatal("project approval crossed source")
		}
	}
}

func TestMCPConnectionApprovalMalformedAndForgedFailClosed(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": {Transport: "http", URL: "https://example.com/mcp"}}}})
	c := loadMCP(t, paths, config.LoadOptions{})
	server := c.MCP.Servers["user:docs"]
	changed := server
	changed.Fingerprint = strings.Repeat("a", 64)
	if commit, err := config.SaveMCPConnectionDecision(t.Context(), paths, changed, config.MCPConnectionAllow); err == nil || commit.Committed {
		t.Fatal("forged fingerprint accepted")
	}
	for _, raw := range []any{nil, map[string]any{"user:docs": map[string]any{"fingerprint": server.Fingerprint, "decision": "allow", "extra": true}}, map[string]any{"user:docs": map[string]any{"fingerprint": "bad", "decision": "allow"}}} {
		writeJSON(t, paths.GlobalAuth, map[string]any{"mcp_connections": raw})
		before, _ := os.ReadFile(paths.GlobalAuth)
		if _, err := config.LoadFiles(paths, config.LoadOptions{}); err == nil {
			t.Fatal("malformed approval loaded")
		}
		if commit, err := config.SaveMCPConnectionDecision(t.Context(), paths, server, config.MCPConnectionAllow); err == nil || commit.Committed {
			t.Fatal("malformed approval overwritten")
		}
		after, _ := os.ReadFile(paths.GlobalAuth)
		if string(before) != string(after) || !json.Valid(after) {
			t.Fatal("failed save changed source")
		}
	}
}
