//go:build integration

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type mcpOAuthRemoteReport struct {
	Server             mcpclient.Info `json:"server"`
	CatalogTools       int            `json:"catalog_tools"`
	Reads              int            `json:"successful_reads"`
	Connections        int            `json:"connections"`
	Refreshes          int            `json:"refresh_callbacks"`
	LocalExpirySet     bool           `json:"local_expiry_set"`
	IdentityPreserved  bool           `json:"identity_preserved"`
	CredentialReloaded bool           `json:"credential_reloaded"`
	AccessRotated      bool           `json:"access_rotated"`
	RefreshRotated     bool           `json:"refresh_rotated"`
	Cleanup            bool           `json:"cleanup"`
	Accepted           bool           `json:"accepted"`
}

// Login is completed separately through the ordinary CLI/browser. This gate
// reads only the current Linear user, then advances this test service's local
// expiry to exercise the real production refresh path. It does not wait for
// server-side expiry, call a model, log account data or save returned content.
func TestLinearOAuthReadRefresh(t *testing.T) {
	mode := os.Getenv("AICE_MCP_LINEAR_OAUTH_TEST")
	if mode != "inspect" && mode != "read-refresh" {
		t.Skip("explicit Linear OAuth inspect/read-refresh opt-in required")
	}
	artifacts := os.Getenv("AICE_MCP_LINEAR_ARTIFACT_DIR")
	entries, err := os.ReadDir(artifacts)
	if !filepath.IsAbs(artifacts) || err != nil || len(entries) != 0 {
		t.Fatal("fresh empty absolute artifact directory required")
	}
	configuration, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatal("cannot load user configuration")
	}
	const key = "user:linear-readonly-validation"
	server, ok := configuration.MCP.Servers[key]
	credential, loggedIn := server.OAuthCredentials()
	if !ok || server.Settings.URL != "https://mcp.linear.app/mcp" || server.Settings.OAuth == nil ||
		server.Settings.OAuth.Scopes == nil || !slices.Equal(*server.Settings.OAuth.Scopes, []string{"read"}) ||
		!loggedIn || !slices.Equal(credential.Scopes, []string{"read"}) || credential.RefreshToken == "" ||
		server.OAuthTokenExpired(time.Now()) || configuration.MCP.ConnectionDecision(key) != config.MCPConnectionAllow {
		t.Fatal("reviewed read-only Linear login and current connection approval required")
	}
	var report mcpOAuthRemoteReport
	defer func() {
		// Server instructions are untrusted content, not evidence to publish.
		report.Server.Instructions = ""
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(artifacts, "report.json"), encoded, 0600) != nil {
			t.Error("cannot retain sanitized OAuth report")
		}
	}()
	mcpOAuthReadRefresh(t, configuration, key, "get_user", json.RawMessage(`{"query":"me"}`), mode == "inspect", &report)
}

func mcpOAuthReadRefresh(t *testing.T, configuration config.Config, key, remote string, args json.RawMessage, inspect bool, report *mcpOAuthRemoteReport) {
	t.Helper()
	gate := ownerTestGuard(t)
	refresh := mcpOAuthRefresh(configuration.Paths)
	owner, err := newMCPOwner(configuration.MCP, gate, false, func(ctx context.Context, settings mcpclient.Config) (mcpOwnedConnection, error) {
		client, err := mcpclient.Open(ctx, settings)
		if err != nil {
			return nil, err
		}
		report.Connections++
		report.Server = client.Info()
		return client, nil
	}, func(ctx context.Context, server config.MCPServer) (config.MCPOAuthCredentials, error) {
		report.Refreshes++
		return refresh(ctx, server)
	})
	if err != nil {
		t.Fatal("cannot create authorized owner")
	}
	defer func() {
		report.Cleanup = owner.Close() == nil
		if !report.Cleanup {
			report.Accepted = false
			t.Error("owned connection cleanup failed")
		}
	}()
	catalog, err := newMCPCatalog(configuration.MCP, owner.Connections(), gate)
	if err != nil {
		t.Fatal("cannot create run catalog")
	}
	defer catalog.Close()
	found, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Service: key, IDs: []string{mcpToolID(key, remote)}, Limit: 1})
	if err != nil || !found.Complete || len(found.Selected) != 1 {
		t.Fatal("reviewed read absent from complete catalog")
	}
	for _, status := range owner.Status() {
		if status.Key == key {
			report.CatalogTools = status.ToolCount
		}
	}
	entries, err := catalog.Resolve(t.Context(), []string{found.Selected[0].ID})
	if err != nil || len(entries) != 1 {
		t.Fatal("cannot resolve reviewed read")
	}
	definition := entries[0].Tool.Definition()
	if inspect {
		t.Logf("reviewed_tool=%s input_schema=%s", remote, definition.InputSchema)
		return // No grant, account read, local expiry edit or refresh in inspect mode.
	}
	binding, scope, known, err := catalog.MCPBinding(t.Context(), definition.Name)
	if err != nil || !known {
		t.Fatal("missing read binding")
	}
	decision, permit, err := gate.CheckMCP(t.Context(), definition.Name, binding, scope)
	if err != nil || decision.Decision != guard.DecisionAsk || permit == nil {
		t.Fatal("expected a fresh read-only Session grant")
	}
	if err := permit.AllowSession(t.Context(), false); err != nil {
		t.Fatal("cannot grant the reviewed read")
	}
	read := func() {
		t.Helper()
		checks := 0
		result, err := owner.Connections()[key].CallChecked(t.Context(), remote, args, func(ctx context.Context) error {
			checks++
			if err := catalog.Check(ctx, found.Selected[0]); err != nil {
				return err
			}
			return permit.Validate(ctx)
		})
		if err != nil || result.State != llm.ExecutionReturned || result.IsError || checks == 0 {
			t.Fatal("authenticated read failed; result content omitted")
		}
		textBytes := 0
		for _, block := range result.Content {
			if block.Kind == mcpclient.BlockText {
				textBytes += len(block.Text)
			}
		}
		if textBytes == 0 {
			t.Fatal("authenticated read returned no text")
		}
		report.Reads++
	}
	read()
	server := configuration.MCP.Servers[key]
	before, _ := server.OAuthCredentials()
	// This deliberately changes expiry metadata only for the separately named
	// validation service. The next normal owner operation performs the exchange.
	expired, _, err := config.RefreshMCPOAuth(t.Context(), configuration.Paths, server, func(_ context.Context, current config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		current.ExpiresAt = time.Now().Add(-time.Second)
		return current, nil
	})
	if err != nil {
		t.Fatal("cannot prepare local expiry boundary")
	}
	report.LocalExpirySet = true
	updated, err := server.WithRefreshedMCPOAuth(expired)
	if err != nil {
		t.Fatal("local expiry changed identity")
	}
	owner.mu.Lock()
	owner.services[key].server = updated
	owner.mu.Unlock()
	read()
	reloaded, err := config.LoadFiles(configuration.Paths, config.LoadOptions{})
	if err != nil {
		t.Fatal("cannot reload persisted refresh")
	}
	next := reloaded.MCP.Servers[key]
	after, ok := next.OAuthCredentials()
	report.IdentityPreserved = ok && before.GrantID == after.GrantID && server.Fingerprint == next.Fingerprint && reloaded.MCP.ConnectionDecision(key) == config.MCPConnectionAllow
	report.CredentialReloaded = ok && !after.ExpiresAt.IsZero() && after.ExpiresAt.After(time.Now())
	report.AccessRotated = before.AccessToken != after.AccessToken
	report.RefreshRotated = before.RefreshToken != after.RefreshToken
	report.Accepted = report.Reads == 2 && report.Connections == 1 && report.Refreshes == 1 && report.IdentityPreserved && report.CredentialReloaded
	if !report.Accepted {
		t.Fatalf("read/refresh/persistence invariants: reads=%d connections=%d refreshes=%d identity=%t reloaded=%t", report.Reads, report.Connections, report.Refreshes, report.IdentityPreserved, report.CredentialReloaded)
	}
}

func TestMCPExternalOAuthHarness(t *testing.T) {
	fixture := newMCPRefreshFixture(t)
	configuration := refreshFixtureConfig(t, fixture, time.Now().Add(time.Hour))
	const key = "user:service0"
	configuration = ownerTestApprove(t, configuration, key, config.MCPConnectionAllow)
	if _, err := config.SaveMCPConnectionDecision(t.Context(), configuration.Paths, configuration.MCP.Servers[key], config.MCPConnectionAllow); err != nil {
		t.Fatal(err)
	}
	var report mcpOAuthRemoteReport
	mcpOAuthReadRefresh(t, configuration, key, "read", json.RawMessage(`{}`), false, &report)
	if !report.Accepted || !report.Cleanup || fixture.refreshes.Load() != 1 || fixture.opens.Load() != 1 || fixture.calls.Load() != 2 || fixture.closes.Load() != 1 {
		t.Fatal("offline peer disagrees with read/refresh accounting")
	}
}
