package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpauth"
)

type mcpOAuthLoginFixture struct {
	url                           string
	mu                            sync.Mutex
	redirect, challenge           string
	probes, registrations, tokens atomic.Int32
}

func newMCPLoginFixture(t *testing.T) *mcpOAuthLoginFixture {
	t.Helper()
	f := &mcpOAuthLoginFixture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			f.probes.Add(1)
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
				t.Error("login performed an authenticated MCP operation")
			}
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.url+`/metadata", scope="read"`)
			w.WriteHeader(401)
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.url + "/mcp", "authorization_servers": []string{f.url}, "scopes_supported": []string{"read", "write"}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.url, "authorization_endpoint": f.url + "/authorize", "token_endpoint": f.url + "/token", "registration_endpoint": f.url + "/register", "code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"}, "token_endpoint_auth_methods_supported": []string{"none"}, "authorization_response_iss_parameter_supported": true})
		case "/register":
			f.registrations.Add(1)
			var in struct {
				Redirects []string `json:"redirect_uris"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Redirects) != 1 {
				http.Error(w, "bad registration", 400)
				return
			}
			f.mu.Lock()
			f.redirect = in.Redirects[0]
			f.mu.Unlock()
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "aice-fixture", "redirect_uris": in.Redirects, "token_endpoint_auth_method": "none"})
		case "/authorize":
			callback := f.callback(t, f.url+r.URL.RequestURI())
			http.Redirect(w, r, callback, http.StatusFound)
		case "/token":
			f.tokens.Add(1)
			if r.ParseForm() != nil {
				t.Error("bad token form")
			}
			f.mu.Lock()
			redirect, challenge := f.redirect, f.challenge
			f.mu.Unlock()
			proof := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if r.Method != "POST" || r.PostForm.Get("code") != "fixture-code" || r.PostForm.Get("redirect_uri") != redirect || r.PostForm.Get("resource") != f.url+"/mcp" || base64.RawURLEncoding.EncodeToString(proof[:]) != challenge {
				t.Error("invalid authorization-code proof")
			}
			_, _ = io.WriteString(w, `{"access_token":"oauth-access-private","refresh_token":"oauth-refresh-private","token_type":"Bearer","expires_in":3600,"scope":"read"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	f.url = srv.URL
	t.Cleanup(srv.Close)
	return f
}

func (f *mcpOAuthLoginFixture) callback(t *testing.T, address string) string {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Get("redirect_uri") != f.redirect || q.Get("resource") != f.url+"/mcp" || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "read" {
		t.Error("authorization binding/scope")
	}
	f.challenge = q.Get("code_challenge")
	return f.redirect + "?" + url.Values{"state": {q.Get("state")}, "iss": {f.url}, "code": {"fixture-code"}}.Encode()
}

func assertMCPCallbackClosed(t *testing.T, redirect string) {
	t.Helper()
	u, _ := url.Parse(redirect)
	connection, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err == nil {
		connection.Close()
		t.Fatal("OAuth callback listener leaked")
	}
}

func loginFixtureConfig(t *testing.T, f *mcpOAuthLoginFixture) config.Config {
	t.Helper()
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"mcp":{"servers":{"docs":{"transport":"http","url":"`+f.url+`/mcp","oauth":{}}}}}`)
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMCPOAuthLoginCLIAndLogout(t *testing.T) {
	f := newMCPLoginFixture(t)
	c := loginFixtureConfig(t, f)
	key := "user:docs"
	original := c.MCP.Servers[key].Fingerprint
	if _, err := managementCommand(t, c.Paths, "", "approve", key, "--fingerprint", original); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run := func(fingerprint string) error {
		command, err := newTestCommand(t, dependencies{
			loadConfig: func(opts config.LoadOptions) (config.Config, error) { return config.LoadFiles(c.Paths, opts) },
			newModel: func(config.Config) (llm.Streamer, error) {
				t.Error("login created model")
				return nil, fmt.Errorf("unexpected model")
			},
			openBrowser: func(ctx context.Context, address string) error {
				req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
				}
				return err
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		stdout.Reset()
		stderr.Reset()
		command.SetOut(&stdout)
		command.SetErr(&stderr)
		command.SetArgs([]string{"mcp", "login", key, "--fingerprint", fingerprint})
		return command.ExecuteContext(t.Context())
	}
	if err := run("stale"); err == nil || f.probes.Load() != 0 {
		t.Fatal("stale fingerprint performed OAuth")
	}
	if err := run(original); err != nil {
		t.Fatal(err)
	}
	var result interaction.MCPResult
	if json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Committed {
		t.Fatal("login result not JSON/committed")
	}
	if !strings.Contains(stderr.String(), f.url+"/authorize?") || strings.Contains(stdout.String(), "state=") {
		t.Fatal("authorization UI mixed with stdout")
	}
	for _, secret := range []string{"oauth-access-private", "oauth-refresh-private", "fixture-code"} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatal("secret exposed in command output")
		}
	}
	loaded, err := config.LoadFiles(c.Paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := loaded.MCP.Servers[key]
	credential, ok := server.OAuthCredentials()
	if !ok || credential.AccessToken != "oauth-access-private" || server.Fingerprint == original || loaded.MCP.ConnectionDecision(key) != config.MCPConnectionAsk {
		t.Fatal("login did not persist separate identity")
	}
	assertMCPCallbackClosed(t, credential.RedirectURI)
	if err := run(server.Fingerprint); err != nil {
		t.Fatal(err)
	}
	newConfig, err := config.LoadFiles(c.Paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newer := newConfig.MCP.Servers[key]
	if newer.Fingerprint == server.Fingerprint || f.tokens.Load() != 2 || f.probes.Load() != 2 {
		t.Fatal("new login reused identity or repeated exchange")
	}
	if _, err := managementCommand(t, c.Paths, "", "logout", key, "--fingerprint", newer.Fingerprint); err != nil {
		t.Fatal(err)
	}
	loggedOut, err := config.LoadFiles(c.Paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loggedOut.MCP.Servers[key].OAuthCredentials(); ok || loggedOut.MCP.ConnectionDecision(key) != config.MCPConnectionAsk {
		t.Fatal("logout retained access")
	}
}

func TestMCPOAuthLoginManualCallbackAndCancel(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"manual", "cancel", "denied", "browser-failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newMCPLoginFixture(t)
			c := loginFixtureConfig(t, f)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			input := make(chan string, 1)
			var callback, redirect string
			notified := 0
			ui := &interaction.AuthInteraction{Input: input, Notify: func(ctx context.Context, p interaction.AuthPrompt) error {
				notified++
				if p.PublicInput || !p.AllowInput {
					t.Error("callback editor was not private")
				}
				if notified == 1 {
					callback = f.callback(t, p.URL)
					u, _ := url.Parse(p.URL)
					redirect = u.Query().Get("redirect_uri")
					switch mode {
					case "cancel":
						cancel()
					case "manual":
						input <- "invalid callback"
					case "denied":
						u, _ := url.Parse(callback)
						q := u.Query()
						q.Del("code")
						q.Set("error", "access_denied")
						u.RawQuery = q.Encode()
						input <- u.String()
					}
				} else {
					input <- callback
				}
				return nil
			}}
			var open func(context.Context, string) error
			if mode == "browser-failure" {
				open = func(context.Context, string) error { return errors.New("private browser error") }
			}
			credential, err := loginMCPOAuth(ctx, c.MCP.Servers["user:docs"], ui, open)
			switch mode {
			case "cancel":
				if !errors.Is(err, context.Canceled) || f.tokens.Load() != 0 {
					t.Fatal("cancel exchanged tokens", err)
				}
			case "denied":
				if !errors.Is(err, mcpauth.ErrDenied) || f.tokens.Load() != 0 {
					t.Fatal("denial exchanged tokens", err)
				}
			default:
				if err != nil || credential.AccessToken != "oauth-access-private" || f.tokens.Load() != 1 || notified != 2 {
					t.Fatal("manual/fallback login", err)
				}
			}
			assertMCPCallbackClosed(t, redirect)
		})
	}
}

func TestMCPOAuthSettingsPublishesLoginAndLogout(t *testing.T) {
	f := newMCPLoginFixture(t)
	runMCPSettingsFixture(t, `{"transport":"http","url":"`+f.url+`/mcp","oauth":{}}`, func(ctx context.Context, s *interactiveSession) {
		input := make(chan string, 1)
		ui := &interaction.AuthInteraction{Input: input, Notify: func(_ context.Context, p interaction.AuthPrompt) error {
			if p.Menu != nil {
				input <- "confirm"
			} else {
				input <- f.callback(t, p.URL)
			}
			return nil
		}}
		old := s.mcp
		revision, _ := s.settingsStatus()
		result, err := s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: "login user:docs", Auth: ui})
		if err != nil || !result.Committed || !result.Applied || s.mcp == old || !old.closed {
			t.Fatal("login not published", err)
		}
		server := s.configuration.MCP.Servers["user:docs"]
		credential, ok := server.OAuthCredentials()
		if !ok || s.configuration.MCP.ConnectionDecision(server.Key) != config.MCPConnectionAsk {
			t.Fatal("login granted connection")
		}
		assertMCPCallbackClosed(t, credential.RedirectURI)
		revision, _ = s.settingsStatus()
		result, err = s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "mcp", Arguments: "logout user:docs", Auth: ui})
		if err != nil || !result.Committed || !result.Applied {
			t.Fatal("logout not published", err)
		}
		if _, ok := s.configuration.MCP.Servers[server.Key].OAuthCredentials(); ok || s.conversation.store != nil {
			t.Fatal("logout retained login or created transcript")
		}
	})
}

func TestMCPOAuthNewLoginRemovesOlderRuntimeScopes(t *testing.T) {
	t.Parallel()
	f := newMCPLoginFixture(t)
	c := loginFixtureConfig(t, f)
	key := "user:docs"
	original := c.MCP.Servers[key].Settings
	credential := config.MCPOAuthCredentials{Resource: f.url + "/mcp", Issuer: f.url, TokenEndpoint: f.url + "/token", ClientID: "fixture", AuthMethod: "none", RedirectURI: "http://127.0.0.1:43123/callback", AccessToken: "old-access", RefreshToken: "old-refresh"}
	saved, _, err := config.SaveMCPOAuthLogin(t.Context(), c.Paths, c.MCP.Servers[key], credential)
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.WithMCPOAuth(key, c.MCP.Servers[key].CredentialScope, saved)
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	oauth := *original.OAuth
	scopes := []string{"read"}
	oauth.Scopes = &scopes
	changed.OAuth = &oauth
	c, err = c.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": changed}})
	if err != nil {
		t.Fatal(err)
	}
	next, result, err := executeMCPManagement(t.Context(), c, nil, interaction.MCPRequest{Action: "login", Key: key, Fingerprint: c.MCP.Servers[key].Fingerprint}, func(context.Context, config.MCPServer) (config.MCPOAuthCredentials, error) { return credential, nil })
	if err != nil || !result.Committed {
		t.Fatal(err)
	}
	restored, err := next.WithMCP(config.MCPSettings{Servers: map[string]config.MCPServerSettings{"docs": original}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.MCP.Servers[key].OAuthCredentials(); ok {
		t.Fatal("new login retained old runtime credential scope")
	}
}
