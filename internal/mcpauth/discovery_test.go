package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDiscoveryRejectsWrongIdentityAndUnsupportedMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		protected, metadata func(map[string]any)
	}{
		{"resource", func(m map[string]any) { m["resource"] = "https://other.example/mcp" }, nil},
		{"multiple issuers", func(m map[string]any) {
			m["authorization_servers"] = []string{"https://one.example", "https://two.example"}
		}, nil},
		{"body bearer", func(m map[string]any) { m["bearer_methods_supported"] = []string{"body"} }, nil},
		{"invalid scope", func(m map[string]any) { m["scopes_supported"] = []string{"read write"} }, nil},
		{"issuer", nil, func(m map[string]any) { m["issuer"] = "https://wrong.example" }},
		{"missing PKCE", nil, func(m map[string]any) { delete(m, "code_challenge_methods_supported") }},
		{"plain PKCE", nil, func(m map[string]any) { m["code_challenge_methods_supported"] = []string{"plain"} }},
		{"implicit only", nil, func(m map[string]any) { m["response_types_supported"] = []string{"token"} }},
		{"device only", nil, func(m map[string]any) {
			m["grant_types_supported"] = []string{"urn:ietf:params:oauth:grant-type:device_code"}
		}},
		{"token user info", nil, func(m map[string]any) { m["token_endpoint"] = "https://secret@example.com/token" }},
		{"token fragment", nil, func(m map[string]any) { m["token_endpoint"] = "https://example.com/token#" }},
		{"insecure token", nil, func(m map[string]any) { m["token_endpoint"] = "http://example.com/token" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuthFixture(t)
			f.protected, f.metadata = tc.protected, tc.metadata
			if _, err := f.client.Discover(context.Background(), Discovery{Resource: f.url + "/mcp"}); !errors.Is(err, ErrMetadata) {
				t.Fatal(err)
			}
			if f.tokens.Load() != 0 || f.registrations.Load() != 0 {
				t.Fatal("invalid discovery exchanged credentials")
			}
		})
	}
}

func TestDiscoveryFallbackOrderAndEscapedIssuerPath(t *testing.T) {
	t.Parallel()
	for _, success := range []string{"/.well-known/oauth-authorization-server/tenant%2Fone", "/.well-known/openid-configuration/tenant%2Fone", "/tenant%2Fone/.well-known/openid-configuration"} {
		t.Run(success, func(t *testing.T) {
			var root string
			paths := make(chan string, 8)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.EscapedPath()
				paths <- path
				if path == "/.well-known/oauth-protected-resource" {
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": root + "/mcp", "authorization_servers": []string{root + "/tenant%2Fone"}})
					return
				}
				if path == success {
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": root + "/tenant%2Fone", "authorization_endpoint": root + "/authorize", "token_endpoint": root + "/token", "code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"}})
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			root = srv.URL
			s, err := (Client{HTTPClient: srv.Client()}).Discover(context.Background(), Discovery{Resource: root + "/mcp"})
			if err != nil || s.Issuer() != root+"/tenant%2Fone" {
				t.Fatal(err)
			}
			want := []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server/tenant%2Fone", "/.well-known/openid-configuration/tenant%2Fone", "/tenant%2Fone/.well-known/openid-configuration"}
			for _, path := range want {
				if got := <-paths; got != path {
					t.Fatalf("got %q want %q", got, path)
				}
				if path == success {
					break
				}
			}
			if len(paths) != 0 {
				t.Fatal("unexpected fallback request")
			}
		})
	}
}

func TestDiscoveryScopeAndIssuerSelection(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	f.protected = func(m map[string]any) {
		m["authorization_servers"] = []string{"https://other.example", f.url + "/tenant"}
	}
	scopes := []string{"special:read"}
	s, err := f.client.Discover(context.Background(), Discovery{Resource: f.url + "/mcp", Issuer: f.url + "/tenant", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	scopes[0] = "changed"
	copyScopes := s.Scopes()
	copyScopes[0] = "changed"
	if !reflect.DeepEqual(s.Scopes(), []string{"special:read"}) {
		t.Fatal("scope alias or challenge restricted to advertised scopes")
	}
	s, err = f.client.Discover(context.Background(), Discovery{Resource: f.url + "/mcp", Issuer: f.url + "/tenant", Scopes: []string{}})
	if err != nil || len(s.Scopes()) != 0 {
		t.Fatal("explicit empty scopes overridden", err)
	}
}

func TestBearerChallengeParsing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		header []string
		want   map[string]string
		err    error
	}{
		{[]string{`Basic realm="x,y", bEaReR scope = "read", resource_metadata="https://example.com/meta", realm="a\"b"`}, map[string]string{"scope": "read", "resource_metadata": "https://example.com/meta", "realm": `a"b`}, nil},
		{[]string{`Digest realm="x", qop="auth,auth-int"`, `Bearer scope=read`}, map[string]string{"scope": "read"}, nil},
		{[]string{`Basic abc==`}, map[string]string{}, nil},
		{[]string{`Bearer scope="read", Scope="write"`}, nil, ErrMetadata},
		{[]string{`Bearer scope="read"`, `Bearer scope="write"`}, nil, ErrMetadata},
		{[]string{`Bearer scope="unterminated`}, nil, ErrMetadata},
		{[]string{"Bearer scope=\"read\"\r\nInjected: value"}, nil, ErrMetadata},
		{[]string{`Bearer scope="read" garbage`}, nil, ErrMetadata},
		{[]string{strings.Repeat("x", (32<<10)+1)}, nil, ErrLimit},
	} {
		got, err := bearerParameters(tc.header)
		if !errors.Is(err, tc.err) || err == nil && !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("challenge parse: got %#v, %v", got, err)
		}
	}
}

func TestBoundedResponsesAndSafeErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"oversized", strings.Repeat(" ", (1<<20)+1), ErrLimit},
		{"trailing", `{"issuer":"secret"} {}`, ErrMetadata},
		{"duplicate", `{"issuer":"first-secret","issuer":"second-secret"}`, ErrMetadata},
		{"case alias", `{"issuer":"first-secret","ISSUER":"second-secret"}`, ErrMetadata},
		{"array", `[]`, ErrMetadata},
		{"null", `null`, ErrMetadata},
		{"invalid UTF8", "{\"issuer\":\"\xff\"}", ErrMetadata},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			_, err := (Client{HTTPClient: srv.Client()}).Discover(context.Background(), Discovery{Resource: srv.URL + "/mcp"})
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
}

func TestProbeDoesNotInheritCookiesOrFollowRedirects(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(f.url)
	jar.SetCookies(u, []*http.Cookie{{Name: "account", Value: "secret"}})
	f.client.HTTPClient.Jar = jar
	if _, err := f.client.Probe(context.Background(), f.url+"/mcp"); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := (Client{HTTPClient: srv.Client()}).Probe(context.Background(), srv.URL)
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != 307 || hits.Load() != 0 {
		t.Fatal("probe redirected", err)
	}
}

func TestCancellationBeforeAndDuringExchange(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	started, exited := make(chan struct{}), make(chan struct{})
	f.tokenReply = func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(exited) }
	a, _, callback := beginTest(t, f.discover(t), "none")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := f.client.Complete(ctx, a, callback); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-exited
	if _, err := f.client.Complete(context.Background(), a, callback); !errors.Is(err, ErrUsed) {
		t.Fatal("canceled request replayed", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := f.client.Discover(ctx, Discovery{Resource: f.url + "/mcp"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInvalidTokenReply(t *testing.T) {
	t.Parallel()
	for _, fields := range []string{`"token_type":"MAC"`, `"expires_in":-1`, `"expires_in":1.5`, `"expires_in":99999999999999`, `"access_token":"line\nbreak"`, `"access_token":"has space"`, `"scope":"read\twrite"`, `"error":"invalid_grant"`} {
		t.Run(fields, func(t *testing.T) {
			f := newOAuthFixture(t)
			f.tokenReply = func(w http.ResponseWriter, _ *http.Request) {
				v := map[string]any{"access_token": "opaque", "token_type": "Bearer"}
				var override map[string]any
				_ = json.Unmarshal([]byte("{"+fields+"}"), &override)
				for k, value := range override {
					v[k] = value
				}
				_ = json.NewEncoder(w).Encode(v)
			}
			a, _, callback := beginTest(t, f.discover(t), "none")
			if _, err := f.client.Complete(context.Background(), a, callback); !errors.Is(err, ErrMetadata) {
				t.Fatal(err)
			}
		})
	}
}

func TestRegistrationAndEndpointValidation(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	s := f.discover(t)
	for _, redirect := range []string{"http://0.0.0.0:1234/callback", "http://localhost:1234/callback", "https://example.com/callback", "http://127.0.0.1:0/callback", "http://127.0.0.1:65536/callback", "http://127.0.0.1:1234/callback?", "http://127.0.0.1:1234/callback#"} {
		if _, err := f.client.Register(context.Background(), s, redirect); !errors.Is(err, ErrConfig) {
			t.Fatal("invalid redirect", redirect, err)
		}
	}
	if f.registrations.Load() != 0 {
		t.Fatal("invalid registration dispatched")
	}
	if endpointFor("https://remote.example", "http://127.0.0.1:1234/token") {
		t.Fatal("HTTPS downgrade")
	}
	if !endpointFor("http://127.0.0.1:1234", "http://127.0.0.1:1234/token") {
		t.Fatal("local HTTP denied")
	}
	if value := fmt.Sprint(Token{AccessToken: "secret-access", RefreshToken: "secret-refresh"}, Registration{ClientSecret: "secret-client"}); strings.Contains(value, "secret-") {
		t.Fatal("credential formatting leaked")
	}
}
