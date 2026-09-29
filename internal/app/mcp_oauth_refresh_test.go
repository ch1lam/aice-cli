package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/tool"
)

type mcpRefreshFixture struct {
	url                                    string
	refreshes, opens, calls, reads, closes atomic.Int32
	reject, failRefresh                    atomic.Bool
}

func newMCPRefreshFixture(t *testing.T) *mcpRefreshFixture {
	t.Helper()
	f := &mcpRefreshFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.url + "/mcp", "authorization_servers": []string{f.url}})
			return
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.url, "authorization_endpoint": f.url + "/authorize", "token_endpoint": f.url + "/token", "code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"}, "token_endpoint_auth_methods_supported": []string{"none"}})
			return
		case "/token":
			n := f.refreshes.Add(1)
			if r.ParseForm() != nil || r.Method != "POST" || r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != fmt.Sprintf("private-refresh-%d", n-1) || r.PostForm.Get("resource") != f.url+"/mcp" {
				t.Error("incorrect refresh binding or repeated rotation")
			}
			if f.failRefresh.Load() {
				w.WriteHeader(503)
				return
			}
			fmt.Fprintf(w, `{"access_token":"private-access-%d","refresh_token":"private-refresh-%d","expires_in":3600,"token_type":"Bearer"}`, n, n)
			return
		case "/mcp":
		default:
			http.NotFound(w, r)
			return
		}
		if r.Method == "GET" {
			w.WriteHeader(405)
			return
		}
		if r.Method == "DELETE" {
			f.closes.Add(1)
			w.WriteHeader(204)
			return
		}
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&rpc) != nil {
			t.Error("invalid MCP request")
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer private-access-%d", f.refreshes.Load()) {
			t.Error("MCP request used stale or unbound access token")
		}
		if len(rpc.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		result := `{}`
		switch rpc.Method {
		case "initialize":
			f.opens.Add(1)
			w.Header().Set("Mcp-Session-Id", "refresh-fixture")
			result = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{},"resources":{}},"serverInfo":{"name":"fixture","version":"1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"read","description":"Read fixture","inputSchema":{"type":"object"}}]}`
		case "resources/list":
			result = `{"resources":[{"uri":"fixture://value","name":"fixture"}]}`
		case "tools/call":
			f.calls.Add(1)
			if f.reject.Load() {
				w.WriteHeader(401)
				return
			}
			result = `{"content":[{"type":"text","text":"private-access-0 private-access-1 private-access-2 private-refresh-2"}],"structuredContent":{"token":"private-access-2","n":9007199254740993}}`
		case "resources/read":
			f.reads.Add(1)
			result = `{"contents":[{"uri":"fixture://value","text":"private-access-2 private-refresh-2"}]}`
		default:
			t.Errorf("unexpected RPC %s", rpc.Method)
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, rpc.ID, result)
	}))
	f.url = server.URL
	t.Cleanup(server.Close)
	return f
}

func refreshFixtureConfig(t *testing.T, f *mcpRefreshFixture, expiry time.Time) config.Config {
	t.Helper()
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"mcp":{"servers":{"service0":{"transport":"http","url":"`+f.url+`/mcp","oauth":{}}}}}`)
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := c.MCP.Servers["user:service0"]
	credential, _, err := config.SaveMCPOAuthLogin(t.Context(), paths, server, config.MCPOAuthCredentials{Resource: f.url + "/mcp", Issuer: f.url, TokenEndpoint: f.url + "/token", ClientID: "public", AuthMethod: "none", RedirectURI: "http://127.0.0.1:43123/callback", AccessToken: "private-access-0", RefreshToken: "private-refresh-0", ExpiresAt: expiry})
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.WithMCPOAuth(server.Key, server.CredentialScope, credential)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func expireMCPRefreshFixture(t *testing.T, c config.Config, owner *mcpOwner) {
	t.Helper()
	owner.mu.Lock()
	s := owner.services["user:service0"]
	server := s.server
	owner.mu.Unlock()
	credential, _, err := config.RefreshMCPOAuth(t.Context(), c.Paths, server, func(_ context.Context, current config.MCPOAuthCredentials) (config.MCPOAuthCredentials, error) {
		current.ExpiresAt = time.Now().Add(-time.Hour)
		return current, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := server.WithRefreshedMCPOAuth(credential)
	if err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	s.server = updated
	owner.mu.Unlock()
}

func TestMCPOAuthRefreshReusesConnectionAndRedactsRotatedResults(t *testing.T) {
	f := newMCPRefreshFixture(t)
	c := refreshFixtureConfig(t, f, time.Now().Add(-time.Hour))
	c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionAllow)
	gate := ownerTestGuard(t)
	owner, err := newMCPOwner(c.MCP, gate, false, nil, mcpOAuthRefresh(c.Paths))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	owner.Status()
	if f.refreshes.Load() != 0 {
		t.Fatal("status refreshed credentials")
	}
	connection := owner.Connections()["user:service0"]
	catalog, err := newMCPCatalog(c.MCP, owner.Connections(), gate)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	found, err := catalog.Search(t.Context(), tool.ToolSearchRequest{Service: "user:service0", Limit: 5})
	if err != nil || len(found.Selected) != 1 {
		t.Fatal("initial refresh/discovery", err)
	}
	selected, err := catalog.Resolve(t.Context(), []string{found.Selected[0].ID})
	if err != nil || len(selected) != 1 {
		t.Fatal("resolve", err)
	}
	binding := selected[0].Tool.(interface{ ToolBinding() llm.ToolBinding }).ToolBinding()
	_, permit, err := gate.CheckMCP(t.Context(), selected[0].Tool.Definition().Name, binding, mcpPermissionScope(c.MCP, "user:service0"))
	if err != nil || permit == nil {
		t.Fatal("missing pre-rotation permission", err)
	}
	if err := permit.AllowSession(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	// Keep the already-selected schema and grant identity while rotating again.
	generation := connection.ToolGeneration()
	expireMCPRefreshFixture(t, c, owner)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := connection.Tools(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.refreshes.Load() != 2 || f.opens.Load() != 1 || connection.ToolGeneration() != generation || catalog.Check(t.Context(), found.Selected[0]) != nil {
		t.Fatal("rotation changed connection/catalog or repeated refresh")
	}
	// Use the mapper with an explicit checked backend. The test's direct call has
	// no Loop grant; production Loop/Guard acceptance is checked by the CLI test.
	server := c.MCP.Servers["user:service0"]
	decision, _, err := gate.CheckMCP(t.Context(), selected[0].Tool.Definition().Name, binding, mcpPermissionScope(c.MCP, "user:service0"))
	if err != nil || decision.Decision != guard.DecisionAllow {
		t.Fatal("rotation invalidated Session grant", err)
	}
	mapped, err := tool.NewMCP(tool.MCPOptions{Definition: selected[0].Tool.Definition(), Binding: binding, Backend: refreshTestBackend{connection}, Secrets: mcpConnectionSecrets(server), ResultSecrets: mcpResultSecrets(connection)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := mapped.Execute(t.Context(), llm.ToolCall{ID: "call", Name: mapped.Definition().Name, Arguments: []byte(`{}`)})
	if err != nil || result.IsError || result.Details.State != llm.ExecutionReturned {
		t.Fatal("mapped call", err)
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("private-access")) || bytes.Contains(encoded, []byte("private-refresh")) || !bytes.Contains(result.Details.StructuredContent, []byte("9007199254740993")) {
		t.Fatal("rotated credential leaked or structured precision changed")
	}
	expireMCPRefreshFixture(t, c, owner)
	resource := connection.(mcpResourceConnection)
	if _, err := resource.Resources(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.ReadResourceChecked(t.Context(), "fixture://value", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	fp := owner.services[server.Key].server.Fingerprint
	owner.mu.Unlock()
	if fp != server.Fingerprint || f.calls.Load() != 1 || f.reads.Load() != 1 || f.refreshes.Load() != 3 {
		t.Fatal("identity or call count changed")
	}
	if err := owner.Close(); err != nil || f.closes.Load() != 1 {
		t.Fatal("cleanup", err)
	}
}

type refreshTestBackend struct{ connection mcpCatalogConnection }

func (b refreshTestBackend) Call(ctx context.Context, name string, args json.RawMessage) (mcpclient.Result, error) {
	return b.connection.CallChecked(ctx, name, args, func(context.Context) error { return nil })
}

func TestMCPOAuthRefreshCLIAndDurableRedaction(t *testing.T) {
	f := newMCPRefreshFixture(t)
	c := refreshFixtureConfig(t, f, time.Now().Add(-time.Hour))
	c.Provider, c.Model, c.DeepSeekAPIKey = string(deepseek.ProviderID), deepseek.ModelV4Flash, "fixture"
	model := &mcpStartupModel{t: t, search: true, wantRemote: true}
	command, err := newTestCommand(t, dependencies{loadConfig: func(config.LoadOptions) (config.Config, error) { return c, nil }, newModel: func(config.Config) (llm.Streamer, error) { return model, nil }})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	command.SetArgs([]string{"--print", "inspect", "--workspace", t.TempDir(), "--session", path, "--yolo", "--no-dep-install", "--no-update-check"})
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data)+out.String(), "private-access-1") || strings.Contains(string(data)+out.String(), "private-refresh-1") || f.calls.Load() != 1 || f.refreshes.Load() != 1 || f.opens.Load() != 1 || f.closes.Load() != 1 {
		t.Fatal("CLI rotation leaked or replayed")
	}
	loaded, err := config.LoadFiles(c.Paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current, ok := loaded.MCP.Servers["user:service0"].OAuthCredentials()
	if !ok || current.AccessToken != "private-access-1" || loaded.MCP.Servers["user:service0"].Fingerprint != c.MCP.Servers["user:service0"].Fingerprint {
		t.Fatal("rotation not durable or changed identity")
	}
}

func TestMCPOAuthRefreshHonorsApprovalAndFailsClosed(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"unapproved", "denied-yolo", "removed", "failed-exchange"} {
		t.Run(mode, func(t *testing.T) {
			f := newMCPRefreshFixture(t)
			c := refreshFixtureConfig(t, f, time.Now().Add(-time.Hour))
			yolo := mode != "unapproved"
			if mode == "denied-yolo" {
				c = ownerTestApprove(t, c, "user:service0", config.MCPConnectionDeny)
			}
			if mode == "removed" {
				if _, err := config.DeleteMCPOAuth(t.Context(), c.Paths, c.MCP.Servers["user:service0"]); err != nil {
					t.Fatal(err)
				}
			}
			f.failRefresh.Store(mode == "failed-exchange")
			owner, err := newMCPOwner(c.MCP, ownerTestGuard(t), yolo, nil, mcpOAuthRefresh(c.Paths))
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			connection := owner.Connections()["user:service0"]
			for range 2 {
				if _, err := connection.Tools(t.Context()); err == nil {
					t.Fatal("failed authentication connected")
				}
			}
			want := int32(0)
			if mode == "failed-exchange" {
				want = 1
			}
			if f.refreshes.Load() != want || f.opens.Load() != 0 || f.calls.Load() != 0 {
				t.Fatal("unauthorized refresh or automatic retry")
			}
		})
	}
}

func TestMCPOAuthRefreshConsumesOtherOwnersRotation(t *testing.T) {
	f := newMCPRefreshFixture(t)
	c := refreshFixtureConfig(t, f, time.Now().Add(-time.Hour))
	var owners []*mcpOwner
	for range 2 {
		o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, nil, mcpOAuthRefresh(c.Paths))
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, o)
		defer o.Close()
	}
	var wg sync.WaitGroup
	for _, o := range owners {
		wg.Go(func() {
			if _, err := o.Connections()["user:service0"].Tools(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.refreshes.Load() != 1 || f.opens.Load() != 2 {
		t.Fatal("stale owner repeated refresh exchange")
	}
}

func TestMCPOAuthRefreshNeverReplaysRejectedTool(t *testing.T) {
	f := newMCPRefreshFixture(t)
	c := refreshFixtureConfig(t, f, time.Now().Add(-time.Hour))
	o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, nil, mcpOAuthRefresh(c.Paths))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	connection := o.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.reject.Store(true)
	result, err := connection.CallChecked(t.Context(), "read", []byte(`{}`), func(context.Context) error { return nil })
	if err == nil || result.State == llm.ExecutionNotDispatched {
		t.Fatal("HTTP rejection lost dispatched state")
	}
	result, err = connection.CallChecked(t.Context(), "read", []byte(`{}`), func(context.Context) error { return nil })
	if err == nil || result.State != llm.ExecutionNotDispatched || f.calls.Load() != 1 || f.refreshes.Load() != 1 || o.Status()[0].State != "needs_auth" {
		t.Fatal("HTTP 401 triggered refresh or replay")
	}
}

func TestMCPOAuthRefreshCancellationAndRevocation(t *testing.T) {
	for _, mode := range []string{"revoke", "close"} {
		t.Run(mode, func(t *testing.T) {
			c := appOAuthConfig(t, time.Now().Add(-time.Hour))
			started := make(chan struct{})
			var opens atomic.Int32
			o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
				opens.Add(1)
				return &mcpOwnedFixture{}, nil
			}, func(ctx context.Context, _ config.MCPServer) (config.MCPOAuthCredentials, error) {
				close(started)
				<-ctx.Done()
				return config.MCPOAuthCredentials{}, ctx.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			done := make(chan error, 1)
			go func() { _, err := o.Connections()["user:service0"].Tools(t.Context()); done <- err }()
			<-started
			if mode == "close" {
				err = o.Close()
			} else {
				err = o.Revoke("user:service0")
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil || opens.Load() != 0 {
					t.Fatal("canceled refresh opened transport")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("refresh cancellation leaked")
			}
		})
	}
}

type mcpRefreshQueuedFixture struct {
	mcpOwnedFixture
	before func()
}

func (f *mcpRefreshQueuedFixture) CallChecked(ctx context.Context, name string, args json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	f.before()
	return f.mcpOwnedFixture.CallChecked(ctx, name, args, check)
}
func TestMCPOAuthExpiryInTransportQueueStopsBeforeDispatch(t *testing.T) {
	f := newMCPRefreshFixture(t)
	c := refreshFixtureConfig(t, f, time.Now().Add(time.Hour))
	client := &mcpRefreshQueuedFixture{}
	o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) { return client, nil }, mcpOAuthRefresh(c.Paths))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	connection := o.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	var expireOnce sync.Once
	client.before = func() { expireOnce.Do(func() { expireMCPRefreshFixture(t, c, o) }) }
	result, err := connection.CallChecked(t.Context(), "read", []byte(`{}`), func(context.Context) error { return nil })
	if err == nil || result.State != llm.ExecutionNotDispatched || f.refreshes.Load() != 0 || client.calls.Load() != 0 {
		t.Fatal("final check performed I/O or dispatched expired token", err)
	}
	// A subsequent explicit operation may refresh before entering the queue.
	result, err = connection.CallChecked(t.Context(), "read", []byte(`{}`), func(context.Context) error { return nil })
	if err != nil || result.State != llm.ExecutionReturned || f.refreshes.Load() != 1 || client.calls.Load() != 1 {
		t.Fatal("subsequent operation failed to refresh", err)
	}
}

func TestMCPOAuthRefreshRejectsChangedGrantAndRetainsBoundedSecrets(t *testing.T) {
	for _, mode := range []string{"changed-grant", "history-bound"} {
		t.Run(mode, func(t *testing.T) {
			c := appOAuthConfig(t, time.Now().Add(-time.Hour))
			var refreshes, opens atomic.Int32
			o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
				opens.Add(1)
				return &mcpOwnedFixture{}, nil
			}, func(_ context.Context, s config.MCPServer) (config.MCPOAuthCredentials, error) {
				refreshes.Add(1)
				credential, _ := s.OAuthCredentials()
				credential.GrantID = strings.Repeat("f", 64)
				credential.ExpiresAt = time.Now().Add(time.Hour)
				return credential, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			if mode == "history-bound" {
				o.services["user:service0"].secrets = append(o.services["user:service0"].secrets, strings.Repeat("x", 4<<20))
			}
			original := len(o.services["user:service0"].secrets)
			for range 2 {
				if _, err := o.Connections()["user:service0"].Tools(t.Context()); err == nil {
					t.Fatal("invalid refresh proceeded")
				}
			}
			want := int32(1)
			if mode == "history-bound" {
				want = 0
			}
			if refreshes.Load() != want || opens.Load() != 0 || len(o.services["user:service0"].secrets) != original {
				t.Fatal("invalid identity/limit admitted, retried, or evicted credentials")
			}
		})
	}
}

func TestMCPOwnerReplacementRedactsLateResultWithRetiredSecrets(t *testing.T) {
	c := appOAuthConfig(t, time.Now().Add(-time.Hour))
	started := make(chan struct{})
	client := &mcpOwnedFixture{mcpCatalogFixture: mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("read", "Read fixture")}}}
	client.call = func(ctx context.Context) (mcpclient.Result, error) {
		close(started)
		<-ctx.Done()
		return mcpclient.Result{State: llm.ExecutionReturned, Content: []mcpclient.Block{{Kind: mcpclient.BlockText, Text: "late rotated-access-secret rotated-refresh-secret"}}}, nil
	}
	o, err := newMCPOwner(c.MCP, ownerTestGuard(t), true, func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
		return client, nil
	}, func(_ context.Context, server config.MCPServer) (config.MCPOAuthCredentials, error) {
		credential, _ := server.OAuthCredentials()
		credential.AccessToken, credential.RefreshToken = "rotated-access-secret", "rotated-refresh-secret"
		credential.ExpiresAt = time.Now().Add(time.Hour)
		return credential, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	connection := o.Connections()["user:service0"]
	if _, err := connection.Tools(t.Context()); err != nil {
		t.Fatal(err)
	}
	server := c.MCP.Servers["user:service0"]
	mapped, err := tool.NewMCP(tool.MCPOptions{
		Definition: llm.ToolDefinition{Name: "read", Description: "Read fixture", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Binding:    llm.ToolBinding{Source: server.Source.Kind + ":" + server.Source.Location, ServiceID: server.ID, ConnectionFingerprint: server.Fingerprint, ToolName: "read", SchemaFingerprint: strings.Repeat("a", 64)},
		Backend:    refreshTestBackend{connection}, ResultSecrets: mcpResultSecrets(connection),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan llm.ToolResult, 1)
	go func() {
		value, _ := mapped.Execute(t.Context(), llm.ToolCall{ID: "late", Name: "read", Arguments: json.RawMessage(`{}`)})
		result <- value
	}()
	<-started
	if cleanup, err := o.reconfigure(c.MCP, "user:service0"); cleanup != nil || err != nil {
		t.Fatal("replacement", cleanup, err)
	}
	returned := <-result
	encoded, err := json.Marshal(returned)
	if err != nil || returned.Details == nil || returned.Details.State != llm.ExecutionReturned || !bytes.Contains(encoded, []byte("late [credential redacted]")) || bytes.Contains(encoded, []byte("rotated-access-secret")) || bytes.Contains(encoded, []byte("rotated-refresh-secret")) {
		t.Fatal("retirement lost returned facts or credential redaction", err)
	}
	if client.calls.Load() != 1 || client.closed.Load() != 1 {
		t.Fatal("replacement replayed call or leaked old client")
	}
}
