package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

func appOAuthConfig(t *testing.T, expiry time.Time) config.Config {
	t.Helper()
	c := config.Config{Paths: trustTestPaths(t)}
	server := config.MCPServerSettings{Transport: "http", URL: "https://example.com/mcp", OAuth: &config.MCPOAuthSettings{}}
	var err error
	c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"service0": server}})
	if err != nil {
		t.Fatal(err)
	}
	saved, commit, err := config.SaveMCPOAuthLogin(t.Context(), c.Paths, c.MCP.Servers["user:service0"], config.MCPOAuthCredentials{
		Resource: server.URL, Issuer: "https://example.com", TokenEndpoint: "https://example.com/token", ClientID: "public-client", AuthMethod: "none", RedirectURI: "http://127.0.0.1:43123/callback", AccessToken: "oauth-access-secret", RefreshToken: "oauth-refresh-secret", ExpiresAt: expiry,
	})
	if err != nil || !commit.Committed {
		t.Fatal(err)
	}
	c, err = c.WithMCPOAuth("user:service0", c.MCP.Servers["user:service0"].CredentialScope, saved)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMCPOwnerUsesBoundOAuthAndRejectsExpiredToken(t *testing.T) {
	t.Parallel()
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "expired-yolo"}[expired], func(t *testing.T) {
			expiry := time.Now().Add(time.Hour)
			if expired {
				expiry = time.Now().Add(-time.Hour)
			}
			c := appOAuthConfig(t, expiry)
			var opens atomic.Int32
			owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(ctx context.Context, wire mcpclient.Config) (mcpOwnedConnection, error) {
				opens.Add(1)
				if wire.HTTP.Authorization == nil || wire.HTTP.Authorization() != "Bearer oauth-access-secret" {
					t.Error("token was not scoped to the MCP connection")
				}
				return &mcpOwnedFixture{}, nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			_, err = owner.Connections()["user:service0"].Tools(t.Context())
			if expired {
				if err == nil || opens.Load() != 0 || owner.Status()[0].State != "needs_auth" {
					t.Fatal("expired token connected", err)
				}
			} else if err != nil || opens.Load() != 1 {
				t.Fatal("valid credential unusable", err)
			}
			secrets := mcpConnectionSecrets(c.MCP.Servers["user:service0"])
			for _, want := range []string{"oauth-access-secret", "oauth-refresh-secret"} {
				found := false
				for _, secret := range secrets {
					found = found || secret == want
				}
				if !found {
					t.Fatal("OAuth credential omitted from redaction")
				}
			}
		})
	}
}

func TestMCPOwnerOAuthExpiryRecheckedAtDispatch(t *testing.T) {
	t.Parallel()
	c := appOAuthConfig(t, time.Now().Add(time.Hour))
	fixture := &mcpOwnedFixture{}
	owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) { return fixture, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	connection := owner.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A pending dispatch observes expiry even after discovery and initial begin.
	server := c.MCP.Servers["user:service0"]
	credential, _ := server.OAuthCredentials()
	credential.ExpiresAt = time.Now().Add(-time.Hour)
	updated, err := c.WithMCPOAuth(server.Key, server.CredentialScope, credential)
	if err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	service := owner.services[server.Key]
	owner.mu.Unlock()
	check := connection.(mcpBorrowedConnection).dispatchCheck(service, fixture, func(context.Context) error { return nil })
	// Expiry changes while a transport's final callback is still queued.
	owner.mu.Lock()
	service.server = updated.MCP.Servers[server.Key]
	owner.mu.Unlock()
	result, err := fixture.CallChecked(t.Context(), "read", json.RawMessage(`{}`), check)
	if err == nil || result.State != llm.ExecutionNotDispatched || fixture.calls.Load() != 0 {
		t.Fatal("expired dispatch ran", err)
	}
}

func TestMCPOAuthClientSecretThroughManagementCLI(t *testing.T) {
	paths := trustTestPaths(t)
	definition := `{"transport":"http","url":"https://example.com/mcp","oauth":{"client_id":"registered","token_endpoint_auth_method":"client_secret_post","redirect_uri":"http://127.0.0.1:43123/callback","client_secret":{"auth_ref":"client-secret"}}}`
	if _, err := managementCommand(t, paths, definition, "add", "docs"); err != nil {
		t.Fatal(err)
	}
	status, err := managementCommand(t, paths, "", "status", "user:docs")
	if err != nil {
		t.Fatal(err)
	}
	result, err := managementCommand(t, paths, "oauth-client-secret", "credential", "user:docs", "client-secret", "--fingerprint", status.Services[0].Fingerprint)
	if err != nil || !result.Committed {
		t.Fatal("OAuth secret slot inaccessible", err)
	}
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.MCP.Servers["user:docs"].OAuthClientSecret() != "oauth-client-secret" {
		t.Fatal("client secret missing")
	}
	status, err = managementCommand(t, paths, "", "status", "user:docs")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "oauth-client-secret") || status.Services[0].State != "needs_auth" {
		t.Fatal("status exposed secret or claimed login ready")
	}
}
