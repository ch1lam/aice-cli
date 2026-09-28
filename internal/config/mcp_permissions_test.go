package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func permissionFixture(t *testing.T) (config.Paths, config.Config, config.MCPPermission) {
	t.Helper()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": httpMCP()}}})
	writeJSON(t, paths.GlobalAuth, map[string]any{"openai_api_key": "untouched-provider"})
	return paths, loadMCP(t, paths, config.LoadOptions{}), config.MCPPermission{Tool: "read", SchemaFingerprint: strings.Repeat("a", 64), Decision: "allow"}
}

func TestMCPPermissionsPersistExactBindingAndIndependentSnapshot(t *testing.T) {
	paths, c, rule := permissionFixture(t)
	server := c.MCP.Servers["user:docs"]
	commit, err := config.SaveMCPPermission(t.Context(), paths, c.MCP, server.Key, rule)
	if err != nil || !commit.Committed {
		t.Fatal(commit, err)
	}
	next, err := c.WithMCPPermission(server.Key, server.Fingerprint, rule)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadMCP(t, paths, config.LoadOptions{})
	for _, bound := range []config.Config{next, loaded} {
		if bound.MCP.PermissionDecision(server.Key, "", "read", rule.SchemaFingerprint) != "allow" || bound.MCP.ConnectionDecision(server.Key) != "ask" {
			t.Fatal("rule lost or connection implicitly approved")
		}
		if bound.MCP.PermissionDecision(server.Key, "", "read", strings.Repeat("b", 64)) != "ask" || bound.MCP.PermissionDecision(server.Key, llm.OperationResourceRead, "read", rule.SchemaFingerprint) != "ask" {
			t.Fatal("rule crossed schema or operation domain")
		}
	}
	if c.MCP.PermissionDecision(server.Key, "", "read", rule.SchemaFingerprint) != "ask" || loaded.OpenAIAPIKey != "untouched-provider" {
		t.Fatal("unrelated snapshot or auth changed")
	}
	rules := next.MCP.Permissions(server.Key)
	rules[0].Decision = "deny"
	if next.MCP.Permissions(server.Key)[0].Decision != "allow" {
		t.Fatal("returned rules alias state")
	}
	for _, change := range []string{"endpoint", "scope", "credential", "source"} {
		t.Run(change, func(t *testing.T) {
			changed := next
			settings := config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": httpMCP()}}
			switch change {
			case "endpoint":
				s := settings.Servers["docs"]
				s.URL = "https://other.example/mcp"
				settings.Servers["docs"] = s
			case "scope":
				settings.Restrictions = []config.MCPRestriction{{Source: "user", Server: "docs", Tools: []string{"write"}}}
			case "credential":
				changed, err = next.WithMCPCredential(server.Key, server.CredentialScope, "token", "new-account")
			case "source":
				changed.MCP = next.MCP.Clone()
				s := changed.MCP.Servers[server.Key]
				delete(changed.MCP.Servers, server.Key)
				s.Key = "project:docs"
				changed.MCP.Servers[s.Key] = s
			}
			if change == "endpoint" || change == "scope" {
				changed, err = next.WithMCP(settings)
			}
			if err != nil || len(changed.MCP.Permissions(server.Key)) != 0 {
				t.Fatal("rule crossed changed binding", err)
			}
		})
	}
	rule.Decision = "deny"
	if _, err := config.SaveMCPPermission(t.Context(), paths, c.MCP, server.Key, rule); err != nil {
		t.Fatal(err)
	}
	denied := loadMCP(t, paths, config.LoadOptions{})
	if denied.MCP.PermissionDecision(server.Key, "", "read", strings.Repeat("b", 64)) != "deny" {
		t.Fatal("schema change lifted explicit deny")
	}
	rule.Decision = "ask"
	if _, err := config.SaveMCPPermission(t.Context(), paths, c.MCP, server.Key, rule); err != nil {
		t.Fatal(err)
	}
	if len(loadMCP(t, paths, config.LoadOptions{}).MCP.Permissions(server.Key)) != 0 {
		t.Fatal("rule not removed")
	}
}

func TestMCPPermissionsUserOnlyAndMalformedPreserved(t *testing.T) {
	paths, c, rule := permissionFixture(t)
	server := c.MCP.Servers["user:docs"]
	paths.ProjectSettings = filepath.Join(t.TempDir(), "settings.json")
	raw := map[string]any{server.Key: map[string]any{"fingerprint": server.Fingerprint, "scope": c.MCP.PermissionScope(server.Key), "rules": []config.MCPPermission{rule}}}
	writeJSON(t, paths.ProjectSettings, map[string]any{"mcp_permissions": raw})
	if got := loadMCP(t, paths, config.LoadOptions{}); len(got.MCP.Permissions(server.Key)) != 0 || len(got.Diagnostics) == 0 {
		t.Fatal("project self authorized")
	}
	for _, invalid := range []any{nil, map[string]any{server.Key: map[string]any{"fingerprint": server.Fingerprint, "scope": c.MCP.PermissionScope(server.Key), "rules": []config.MCPPermission{rule, rule}}}, map[string]any{"bad": "secret-value"}} {
		writeJSON(t, paths.GlobalAuth, map[string]any{"mcp_permissions": invalid})
		before, _ := os.ReadFile(paths.GlobalAuth)
		if _, err := config.LoadFiles(paths, config.LoadOptions{}); err == nil {
			t.Fatal("malformed rules loaded")
		}
		if commit, err := config.SaveMCPPermission(t.Context(), paths, c.MCP, server.Key, rule); err == nil || commit.Committed || strings.Contains(err.Error(), "secret-value") {
			t.Fatal("malformed rules overwritten or exposed")
		}
		after, _ := os.ReadFile(paths.GlobalAuth)
		if string(before) != string(after) {
			t.Fatal("failed write changed file")
		}
	}
}

func TestMCPPermissionConcurrentWritersAndRemoval(t *testing.T) {
	paths, c, rule := permissionFixture(t)
	var wg sync.WaitGroup
	for _, name := range []string{"read", "write"} {
		wg.Go(func() {
			r := rule
			r.Tool = name
			if _, err := config.SaveMCPPermission(t.Context(), paths, c.MCP, "user:docs", r); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(loadMCP(t, paths, config.LoadOptions{}).MCP.Permissions("user:docs")) != 2 {
		t.Fatal("concurrent rule lost")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if commit, err := config.SaveMCPPermission(ctx, paths, c.MCP, "user:docs", rule); err == nil || commit.Committed {
		t.Fatal("canceled write committed")
	}
	if _, err := config.ForgetMCPServiceAccess(t.Context(), paths, c.MCP.Servers["user:docs"]); err != nil {
		t.Fatal(err)
	}
	if len(loadMCP(t, paths, config.LoadOptions{}).MCP.Permissions("user:docs")) != 0 {
		t.Fatal("removal retained rules")
	}
}

func TestMCPPermissionsOAuthRotationAndNewLogin(t *testing.T) {
	c := newOAuthConfig(t, t.TempDir(), "https://example.com/mcp")
	c = saveOAuthLogin(t, c, "user:docs", oauthCredential("https://example.com/mcp"))
	server := c.MCP.Servers["user:docs"]
	rule := config.MCPPermission{Tool: "read", SchemaFingerprint: strings.Repeat("a", 64), Decision: "allow"}
	if _, err := config.SaveMCPPermission(t.Context(), c.Paths, c.MCP, server.Key, rule); err != nil {
		t.Fatal(err)
	}
	rotated, _, err := config.RefreshMCPOAuth(t.Context(), c.Paths, server, func(_ context.Context, credential config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		credential.AccessToken = "rotated-access"
		return credential, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadMCP(t, c.Paths, config.LoadOptions{})
	if loaded.MCP.PermissionDecision(server.Key, "", "read", rule.SchemaFingerprint) != "allow" {
		t.Fatal("rotation invalidated user rule")
	}
	saved, commit, err := config.SaveMCPOAuthLogin(t.Context(), c.Paths, loaded.MCP.Servers[server.Key], rotated)
	if err != nil || !commit.Committed {
		t.Fatal(err)
	}
	loaded = loadMCP(t, c.Paths, config.LoadOptions{})
	if len(loaded.MCP.Permissions(server.Key)) != 0 {
		t.Fatal("new login inherited rule")
	}
	// The writer removes old records as well as making them inapplicable.
	data, _ := os.ReadFile(c.Paths.GlobalAuth)
	if strings.Contains(string(data), rule.SchemaFingerprint) {
		t.Fatal("new login retained old rule")
	}
	if _, err := config.SaveMCPPermission(t.Context(), c.Paths, loaded.MCP, server.Key, rule); err != nil {
		t.Fatal(err)
	}
	if _, err := config.DeleteMCPOAuth(t.Context(), c.Paths, loaded.MCP.Servers[server.Key]); err != nil {
		t.Fatal(err)
	}
	without, err := loaded.WithoutMCPOAuth(server.Key)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := without.WithMCPOAuth(server.Key, server.CredentialScope, saved)
	if err != nil || len(restored.MCP.Permissions(server.Key)) != 0 {
		t.Fatal("logout projection retained old rule", err)
	}
	data, _ = os.ReadFile(c.Paths.GlobalAuth)
	if strings.Contains(string(data), rule.SchemaFingerprint) {
		t.Fatal("logout retained old rule")
	}
}

func TestMCPInactivePermissionReviewAndRemoval(t *testing.T) {
	paths, c, rule := permissionFixture(t)
	server := c.MCP.Servers["user:docs"]
	for _, name := range []string{"read", "write"} {
		rule.Tool = name
		if _, err := config.SaveMCPPermission(t.Context(), paths, c.MCP, server.Key, rule); err != nil {
			t.Fatal(err)
		}
	}
	c = loadMCP(t, paths, config.LoadOptions{})
	settings := config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": httpMCP()}, Restrictions: []config.MCPRestriction{{Source: "user", Server: "docs", Tools: []string{"write"}}}}
	changed, err := c.WithMCP(settings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, scope, saved := changed.MCP.StoredPermissions(server.Key)
	if fingerprint != server.Fingerprint || scope != c.MCP.PermissionScope(server.Key) || len(saved) != 2 || len(changed.MCP.Permissions(server.Key)) != 0 {
		t.Fatal("inactive rules not distinguishable")
	}
	rule.Tool, rule.Decision = "read", "ask"
	if _, err := config.SaveMCPPermission(t.Context(), paths, changed.MCP, server.Key, rule); err != nil {
		t.Fatal(err)
	}
	_, _, saved = loadMCP(t, paths, config.LoadOptions{}).MCP.StoredPermissions(server.Key)
	if len(saved) != 1 || saved[0].Tool != "write" {
		t.Fatal("removing one inactive rule changed another")
	}
}
