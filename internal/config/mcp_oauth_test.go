package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
)

func oauthMCP(endpoint string) config.MCPServerSettings {
	return config.MCPServerSettings{Transport: "http", URL: endpoint, OAuth: &config.MCPOAuthSettings{}}
}
func oauthCredential(endpoint string) config.MCPOAuthCredentials {
	u, _ := url.Parse(endpoint)
	origin := u.Scheme + "://" + u.Host
	return config.MCPOAuthCredentials{Resource: endpoint, Issuer: origin, TokenEndpoint: origin + "/token", ClientID: "registered-client", AuthMethod: "none", RedirectURI: "http://127.0.0.1:43123/callback", AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour).UTC(), Scopes: []string{"read"}}
}
func saveOAuthLogin(t *testing.T, c config.Config, key string, credential config.MCPOAuthCredentials) config.Config {
	t.Helper()
	server := c.MCP.Servers[key]
	saved, commit, err := config.SaveMCPOAuthLogin(t.Context(), c.Paths, server, credential)
	if err != nil || !commit.Committed {
		t.Fatalf("login save: %v, %+v", err, commit)
	}
	next, err := c.WithMCPOAuth(key, server.CredentialScope, saved)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func newOAuthConfig(t *testing.T, root, endpoint string) config.Config {
	t.Helper()
	paths := testPaths(root)
	writeJSON(t, paths.GlobalSettings, map[string]any{"mcp": config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": oauthMCP(endpoint)}}})
	writeJSON(t, paths.GlobalAuth, map[string]any{"openai_api_key": "provider-secret"})
	return loadMCP(t, paths, config.LoadOptions{})
}

func TestMCPOAuthIdentityRotationAndLogout(t *testing.T) {
	t.Parallel()
	c := newOAuthConfig(t, t.TempDir(), "https://example.com/mcp")
	key := "user:docs"
	initial := c.MCP.Servers[key]
	if !reflect.DeepEqual(initial.MissingValues, []string{"oauth"}) {
		t.Fatal(initial.MissingValues)
	}
	c = saveOAuthLogin(t, c, key, oauthCredential(initial.Settings.URL))
	server := c.MCP.Servers[key]
	if server.Fingerprint == initial.Fingerprint || len(server.MissingValues) != 0 {
		t.Fatal("login did not bind identity")
	}
	if _, err := config.SaveMCPConnectionDecision(t.Context(), c.Paths, server, config.MCPConnectionAllow); err != nil {
		t.Fatal(err)
	}
	c = loadMCP(t, c.Paths, config.LoadOptions{})
	before, _ := server.OAuthCredentials()
	rotated, commit, err := config.RefreshMCPOAuth(t.Context(), c.Paths, server, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		v.AccessToken = "rotated-access"
		v.RefreshToken = "rotated-refresh"
		v.ExpiresAt = v.ExpiresAt.Add(time.Hour)
		return v, nil
	})
	if err != nil || !commit.Committed || rotated.GrantID != before.GrantID {
		t.Fatal("rotation", err)
	}
	rotatedConfig := loadMCP(t, c.Paths, config.LoadOptions{})
	bound := rotatedConfig.MCP.Servers[key]
	if bound.Fingerprint != server.Fingerprint || rotatedConfig.MCP.ConnectionDecision(key) != config.MCPConnectionAllow {
		t.Fatal("rotation invalidated login approval")
	}
	_, headers := bound.ConnectionValues()
	if headers["Authorization"] != "Bearer rotated-access" {
		t.Fatal("new token not resolved")
	}
	_, oldHeaders := server.ConnectionValues()
	if oldHeaders["Authorization"] != "Bearer access-secret" {
		t.Fatal("old snapshot mutated")
	}
	copy := rotatedConfig.MCP.Clone()
	_, copyHeaders := copy.Servers[key].ConnectionValues()
	copyHeaders["Authorization"] = "changed"
	copyCred, _ := copy.Servers[key].OAuthCredentials()
	copyCred.Scopes[0] = "changed"
	original, _ := bound.OAuthCredentials()
	if original.Scopes[0] != "read" || copy.Servers[key].Fingerprint != bound.Fingerprint {
		t.Fatal("clone changed binding or aliases credential")
	}
	if _, err := config.SaveMCPConnectionDecision(t.Context(), c.Paths, copy.Servers[key], config.MCPConnectionAllow); err != nil {
		t.Fatal("cloned identity cannot be saved", err)
	}
	encoded, _ := json.Marshal(rotatedConfig.MCP)
	if strings.Contains(string(encoded), "rotated-") || strings.Contains(fmt.Sprint(rotatedConfig.MCP), "rotated-") {
		t.Fatal("credential exposed")
	}
	second := saveOAuthLogin(t, rotatedConfig, key, original)
	secondServer := second.MCP.Servers[key]
	secondCred, _ := secondServer.OAuthCredentials()
	if secondCred.GrantID == original.GrantID || secondServer.Fingerprint == bound.Fingerprint {
		t.Fatal("second login reused grant")
	}
	if loaded := loadMCP(t, c.Paths, config.LoadOptions{}); loaded.MCP.ConnectionDecision(key) != config.MCPConnectionAsk {
		t.Fatal("new login retained approval")
	}
	called := false
	if _, commit, err := config.RefreshMCPOAuth(t.Context(), c.Paths, bound, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		called = true
		return v, nil
	}); err == nil || commit.Committed || called {
		t.Fatal("replaced login was refreshed")
	}
	if _, err := config.DeleteMCPOAuth(t.Context(), c.Paths, secondServer); err != nil {
		t.Fatal(err)
	}
	loggedOut := loadMCP(t, c.Paths, config.LoadOptions{})
	_, ok := loggedOut.MCP.Servers[key].OAuthCredentials()
	if ok || loggedOut.MCP.ConnectionDecision(key) != config.MCPConnectionAsk || loggedOut.OpenAIAPIKey != "provider-secret" {
		t.Fatal("logout did not isolate credentials")
	}
	if _, _, err := config.RefreshMCPOAuth(t.Context(), c.Paths, secondServer, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		called = true
		return v, nil
	}); err == nil || called {
		t.Fatal("logout resurrected by old refresh")
	}
}

func TestMCPOAuthSourceScopeAndClientSecretIsolation(t *testing.T) {
	t.Parallel()
	c := newOAuthConfig(t, t.TempDir(), "https://example.com/mcp")
	c.Paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	settings := config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": oauthMCP("https://example.com/mcp")}}
	writeJSON(t, c.Paths.ProjectSettings, map[string]any{"mcp": settings, "mcp_oauth": map[string]any{"forged": "secret"}})
	c = loadMCP(t, c.Paths, config.LoadOptions{})
	c = saveOAuthLogin(t, c, "user:docs", oauthCredential("https://example.com/mcp"))
	for _, s := range c.MCP.Servers {
		if s.Source.Kind == "project" {
			if _, ok := s.OAuthCredentials(); ok {
				t.Fatal("cross-source credential")
			}
		}
	}
	if len(c.Diagnostics) != 1 {
		t.Fatal("project OAuth not diagnosed")
	}
	changed := settings.Servers["docs"]
	changed.URL = "https://other.example/mcp"
	settings.Servers["docs"] = changed
	moved, err := c.WithMCP(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := moved.MCP.Servers["user:docs"].OAuthCredentials(); ok {
		t.Fatal("endpoint inherited credential")
	}
	// Confidential client secrets use the same scoped slots as header values.
	scopes := []string{"read"}
	changed = oauthMCP("https://example.com/mcp")
	changed.OAuth = &config.MCPOAuthSettings{ClientID: "confidential", AuthMethod: "client_secret_post", RedirectURI: "http://127.0.0.1:43123/callback", ClientSecret: &config.MCPValueRef{AuthRef: "client-secret"}, Scopes: &scopes}
	settings.Servers["docs"] = changed
	c, err = c.WithMCP(settings)
	if err != nil {
		t.Fatal(err)
	}
	server := c.MCP.Servers["user:docs"]
	if !reflect.DeepEqual(server.CredentialSlots(), []string{"client-secret"}) {
		t.Fatal("OAuth secret slot unavailable")
	}
	c, err = c.WithMCPCredential(server.Key, server.CredentialScope, "client-secret", "client-private")
	if err != nil {
		t.Fatal(err)
	}
	credential := oauthCredential(changed.URL)
	credential.ClientID = "confidential"
	credential.AuthMethod = "client_secret_post"
	credential.ClientSecret = "client-private"
	c = saveOAuthLogin(t, c, server.Key, credential)
	server = c.MCP.Servers[server.Key]
	copy := c.MCP.Clone()
	copyServer := copy.Servers[server.Key]
	(*copyServer.Settings.OAuth.Scopes)[0] = "write"
	copyServer.Settings.OAuth.ClientSecret.AuthRef = "wrong"
	if (*server.Settings.OAuth.Scopes)[0] != "read" || server.Settings.OAuth.ClientSecret.AuthRef != "client-secret" {
		t.Fatal("OAuth settings alias")
	}
	next, err := c.WithMCPCredential(server.Key, server.CredentialScope, "client-secret", "other-client-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := next.MCP.Servers[server.Key].OAuthCredentials(); ok || next.MCP.Servers[server.Key].Fingerprint == server.Fingerprint {
		t.Fatal("client secret replacement retained old login")
	}
}

func TestMCPOAuthFailedRefreshPreservesDisk(t *testing.T) {
	t.Parallel()
	c := newOAuthConfig(t, t.TempDir(), "https://example.com/mcp")
	c = saveOAuthLogin(t, c, "user:docs", oauthCredential("https://example.com/mcp"))
	server := c.MCP.Servers["user:docs"]
	before, _ := os.ReadFile(c.Paths.GlobalAuth)
	for _, mutate := range []func(*config.MCPOAuthCredentials){func(v *config.MCPOAuthCredentials) { v.Issuer = "https://other.example" }, func(v *config.MCPOAuthCredentials) { v.ClientID = "other" }, func(v *config.MCPOAuthCredentials) { v.GrantID = strings.Repeat("b", 64) }, func(v *config.MCPOAuthCredentials) { v.TokenEndpoint = "https://other.example/token" }} {
		_, commit, err := config.RefreshMCPOAuth(t.Context(), c.Paths, server, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
			mutate(&v)
			return v, nil
		})
		if err == nil || commit.Committed {
			t.Fatal("refresh changed identity")
		}
		after, _ := os.ReadFile(c.Paths.GlobalAuth)
		if string(before) != string(after) {
			t.Fatal("invalid refresh modified disk")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, commit, err := config.RefreshMCPOAuth(ctx, c.Paths, server, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		cancel()
		v.AccessToken = "unsaved"
		return v, nil
	})
	if !errors.Is(err, context.Canceled) || commit.Committed {
		t.Fatal("canceled refresh committed", err)
	}
	after, _ := os.ReadFile(c.Paths.GlobalAuth)
	if string(before) != string(after) {
		t.Fatal("canceled refresh changed disk")
	}
	unchanged, commit, err := config.RefreshMCPOAuth(t.Context(), c.Paths, server, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		return v, nil
	})
	if err != nil || commit.Committed || unchanged.GrantID == "" {
		t.Fatal("unchanged credential was rewritten", err)
	}
	if _, err := os.Stat(c.Paths.GlobalAuth + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock retained", err)
	}
}

func TestMCPOAuthMalformedAndRemoval(t *testing.T) {
	t.Parallel()
	c := newOAuthConfig(t, t.TempDir(), "https://example.com/mcp")
	c = saveOAuthLogin(t, c, "user:docs", oauthCredential("https://example.com/mcp"))
	server := c.MCP.Servers["user:docs"]
	if _, err := config.ForgetMCPServiceAccess(t.Context(), c.Paths, server); err != nil {
		t.Fatal(err)
	}
	reloaded := loadMCP(t, c.Paths, config.LoadOptions{})
	if _, ok := reloaded.MCP.Servers[server.Key].OAuthCredentials(); ok {
		t.Fatal("remove left OAuth state")
	}
	for _, raw := range []any{nil, []any{}, map[string]any{"user:docs": map[string]any{"invalid": "secret-value"}}} {
		writeJSON(t, c.Paths.GlobalAuth, map[string]any{"mcp_oauth": raw})
		before, _ := os.ReadFile(c.Paths.GlobalAuth)
		if _, err := config.LoadFiles(c.Paths, config.LoadOptions{}); err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatal("unsafe malformed error", err)
		}
		if _, commit, err := config.SaveMCPOAuthLogin(t.Context(), c.Paths, server, oauthCredential(server.Settings.URL)); err == nil || commit.Committed {
			t.Fatal("malformed state overwritten")
		}
		after, _ := os.ReadFile(c.Paths.GlobalAuth)
		if string(before) != string(after) {
			t.Fatal("malformed file changed")
		}
	}
}

func TestMCPOAuthSettingsValidation(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"client_secret":{"value":"secret-value"}}`,
		`{"client_id":"id"}`,
		`{"client_id":"id","redirect_uri":"http://localhost:43123/callback"}`,
		`{"issuer":"https://user:secret-value@example.com"}`,
		`{"issuer":"https://example.com?tenant=one"}`,
		`{"scopes":["read write"]}`,
		`{"token_endpoint_auth_method":"private_key_jwt"}`,
		`{"client_id":"id","redirect_uri":"http://127.0.0.1:43123/callback","token_endpoint_auth_method":"client_secret_post","client_secret":{"auth_ref":"client-secret","prefix":"secret-value"}}`,
	} {
		var oauth config.MCPOAuthSettings
		if err := json.Unmarshal([]byte(raw), &oauth); err != nil {
			t.Fatal(err)
		}
		server := oauthMCP("https://example.com/mcp")
		server.OAuth = &oauth
		if err := (config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}}).Validate(); err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatal("invalid OAuth settings accepted or leaked", err)
		}
	}
	for _, server := range []config.MCPServerSettings{
		{Transport: "stdio", Command: filepath.Join(t.TempDir(), "helper"), Cwd: t.TempDir(), OAuth: &config.MCPOAuthSettings{}},
		{Transport: "http", URL: "https://example.com/mcp", OAuth: &config.MCPOAuthSettings{}, Headers: map[string]config.MCPValueRef{"authorization": {AuthRef: "token"}}},
	} {
		if err := (config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": server}}).Validate(); err == nil {
			t.Fatal("mixed OAuth/transport authentication accepted")
		}
	}
}

func TestMCPOAuthMetadataIdentityChangeInvalidatesApproval(t *testing.T) {
	t.Parallel()
	c := newOAuthConfig(t, t.TempDir(), "https://example.com/mcp")
	c = saveOAuthLogin(t, c, "user:docs", oauthCredential("https://example.com/mcp"))
	server := c.MCP.Servers["user:docs"]
	credential, _ := server.OAuthCredentials()
	credential.TokenEndpoint = "https://example.com/changed-token"
	writeJSON(t, c.Paths.GlobalAuth, map[string]any{
		"mcp_oauth":       map[string]any{server.Key: map[string]any{server.CredentialScope: credential}},
		"mcp_connections": map[string]any{server.Key: map[string]any{"fingerprint": server.Fingerprint, "decision": "allow"}},
	})
	fresh := loadMCP(t, c.Paths, config.LoadOptions{})
	if fresh.MCP.Servers[server.Key].Fingerprint == server.Fingerprint || fresh.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
		t.Fatal("changed OAuth endpoint reused approval")
	}
}

func TestMCPOAuthStoredCredentialRejectsHTTPSDowngrade(t *testing.T) {
	t.Parallel()
	c := newOAuthConfig(t, t.TempDir(), "http://127.0.0.1:43123/mcp")
	credential := oauthCredential("http://127.0.0.1:43123/mcp")
	credential.Issuer = "HTTPS://auth.example.com"
	if _, commit, err := config.SaveMCPOAuthLogin(t.Context(), c.Paths, c.MCP.Servers["user:docs"], credential); err == nil || commit.Committed {
		t.Fatal("case-insensitive HTTPS issuer downgraded to HTTP token exchange")
	}
}

func TestMCPOAuthConcurrentRefreshProcess(t *testing.T) {
	root := os.Getenv("AICE_TEST_MCP_OAUTH_ROOT")
	if root == "" {
		return
	}
	c := loadMCP(t, testPaths(root), config.LoadOptions{})
	server := c.MCP.Servers["user:docs"]
	_, _, err := config.RefreshMCPOAuth(t.Context(), c.Paths, server, func(ctx context.Context, v config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		if time.Until(v.ExpiresAt) > time.Minute {
			return v, nil
		}
		form := url.Values{"refresh_token": {v.RefreshToken}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.TokenEndpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return v, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return v, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return v, fmt.Errorf("fixture rejected duplicate refresh")
		}
		v.AccessToken = "rotated-access"
		v.RefreshToken = "rotated-refresh"
		v.ExpiresAt = time.Now().Add(time.Hour).UTC()
		return v, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMCPOAuthRefreshSerializedAcrossProcesses(t *testing.T) {
	t.Parallel()
	var exchanges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil || r.Form.Get("refresh_token") != "refresh-secret" || exchanges.Add(1) != 1 {
			http.Error(w, "invalid rotation", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	root := t.TempDir()
	c := newOAuthConfig(t, root, srv.URL+"/mcp")
	credential := oauthCredential(srv.URL + "/mcp")
	credential.ExpiresAt = time.Now().Add(-time.Hour).UTC()
	c = saveOAuthLogin(t, c, "user:docs", credential)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMCPOAuthConcurrentRefreshProcess$")
			cmd.Env = append(os.Environ(), "AICE_TEST_MCP_OAUTH_ROOT="+root)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("refresh process: %v\n%s", err, output)
			}
		})
	}
	wg.Wait()
	if exchanges.Load() != 1 {
		t.Fatal("refresh exchanges", exchanges.Load())
	}
	final := loadMCP(t, c.Paths, config.LoadOptions{})
	latest, ok := final.MCP.Servers["user:docs"].OAuthCredentials()
	if !ok || latest.RefreshToken != "rotated-refresh" || final.OpenAIAPIKey != "provider-secret" {
		t.Fatal("rotation lost credential state")
	}
}
