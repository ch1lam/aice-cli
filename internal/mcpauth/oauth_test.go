package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type oauthFixture struct {
	url           string
	client        Client
	requests      chan url.Values
	tokens        atomic.Int32
	registrations atomic.Int32
	metadata      func(map[string]any)
	protected     func(map[string]any)
	tokenReply    func(http.ResponseWriter, *http.Request)
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{requests: make(chan url.Values, 32)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("probe sent credentials")
			}
			w.Header().Add("WWW-Authenticate", `Basic realm="other,service", Bearer resource_metadata="`+f.url+`/metadata", scope="read"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/metadata", "/.well-known/oauth-protected-resource/mcp":
			v := map[string]any{"resource": f.url + "/mcp", "authorization_servers": []string{f.url + "/tenant"}, "scopes_supported": []string{"read", "write"}, "bearer_methods_supported": []string{"header"}}
			if f.protected != nil {
				f.protected(v)
			}
			_ = json.NewEncoder(w).Encode(v)
		case "/.well-known/oauth-authorization-server/tenant":
			v := map[string]any{"issuer": f.url + "/tenant", "authorization_endpoint": f.url + "/authorize", "token_endpoint": f.url + "/token", "registration_endpoint": f.url + "/register", "code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"}, "authorization_response_iss_parameter_supported": true}
			if f.metadata != nil {
				f.metadata(v)
			}
			_ = json.NewEncoder(w).Encode(v)
		case "/register":
			f.registrations.Add(1)
			var v map[string]any
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&v) != nil {
				t.Error("invalid registration")
			}
			if v["client_name"] != "AICE" || v["token_endpoint_auth_method"] != "none" {
				t.Error("registration identity/method")
			}
			v["client_id"] = "public-client"
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(v)
		case "/token":
			f.tokens.Add(1)
			if r.Method != http.MethodPost || r.ParseForm() != nil {
				t.Error("invalid token request")
			}
			values := r.PostForm
			values["_authorization"] = []string{r.Header.Get("Authorization")}
			f.requests <- values
			if f.tokenReply != nil {
				f.tokenReply(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","expires_in":120,"scope":"read"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f.url, f.client = server.URL, Client{HTTPClient: server.Client()}
	return f
}

func (f *oauthFixture) discover(t *testing.T) Server {
	t.Helper()
	in, err := f.client.Probe(context.Background(), f.url+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.client.Discover(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func beginTest(t *testing.T, s Server, method string) (*Attempt, Registration, string) {
	t.Helper()
	secret := ""
	if method != "none" {
		secret = "secret: with symbols+"
	}
	r, err := s.PreRegistered("client: with symbols+", secret, method, "http://127.0.0.1:43123/callback")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Begin(r)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(a.AuthorizationURL())
	query := url.Values{"code": {"one-use-code"}, "state": {u.Query().Get("state")}, "iss": {s.Issuer()}}
	return a, r, r.RedirectURI + "?" + query.Encode()
}

func TestAuthorizationPKCEAndRefresh(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	s := f.discover(t)
	if !reflect.DeepEqual(s.Scopes(), []string{"read"}) {
		t.Fatal("challenge scope was not authoritative")
	}
	if f.tokens.Load() != 0 || f.registrations.Load() != 0 {
		t.Fatal("discovery performed a credential exchange")
	}
	r, err := f.client.Register(context.Background(), s, "http://127.0.0.1:43123/callback")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Begin(r)
	if err != nil {
		t.Fatal(err)
	}
	authorize, _ := url.Parse(a.AuthorizationURL())
	query := authorize.Query()
	if query.Get("scope") != "read" || query.Get("resource") != f.url+"/mcp" || query.Get("code_challenge_method") != "S256" || len(query.Get("state")) < 43 {
		t.Fatal("authorization parameters", query)
	}
	callback := r.RedirectURI + "?" + url.Values{"state": {query.Get("state")}, "iss": {s.Issuer()}, "code": {"one-use-code"}}.Encode()
	before := time.Now()
	token, err := f.client.Complete(context.Background(), a, callback)
	if err != nil {
		t.Fatal(err)
	}
	request := <-f.requests
	challenge := sha256.Sum256([]byte(request.Get("code_verifier")))
	if query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || len(request.Get("code_verifier")) < 43 {
		t.Fatal("PKCE proof did not match")
	}
	if request.Get("grant_type") != "authorization_code" || request.Get("resource") != s.Resource() || request.Get("redirect_uri") != r.RedirectURI || request.Get("client_id") != r.ClientID || request.Get("code") != "one-use-code" || request.Get("_authorization") != "" {
		t.Fatal("incorrect token request", request)
	}
	if token.Binding != s.binding() || token.ClientID != r.ClientID || token.ExpiresAt.Before(before.Add(119*time.Second)) {
		t.Fatal("token binding/expiry")
	}
	if _, err := f.client.Complete(context.Background(), a, callback); !errors.Is(err, ErrUsed) {
		t.Fatal("code reused", err)
	}
	// Missing rotated refresh_token preserves the previous one; a later rotation replaces it.
	f.tokenReply = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":60}`))
	}
	refreshed, err := f.client.Refresh(context.Background(), s, r, token)
	if err != nil {
		t.Fatal(err)
	}
	request = <-f.requests
	if request.Get("grant_type") != "refresh_token" || request.Get("refresh_token") != "refresh-secret" || request.Get("resource") != s.Resource() || request.Get("code") != "" {
		t.Fatal("incorrect refresh request")
	}
	if refreshed.RefreshToken != token.RefreshToken || refreshed.AccessToken == token.AccessToken || !reflect.DeepEqual(refreshed.Scopes, token.Scopes) {
		t.Fatal("refresh lost credential state")
	}
	f.tokenReply = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"next-access","refresh_token":"next-refresh","token_type":"Bearer"}`))
	}
	rotated, err := f.client.Refresh(context.Background(), s, r, refreshed)
	if err != nil || rotated.RefreshToken != "next-refresh" || !rotated.ExpiresAt.IsZero() {
		t.Fatal("rotation/unknown expiry", err)
	}
}

func TestClientAuthenticationMethods(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			f := newOAuthFixture(t)
			s := f.discover(t)
			a, r, callback := beginTest(t, s, method)
			if _, err := f.client.Complete(context.Background(), a, callback); err != nil {
				t.Fatal(err)
			}
			request := <-f.requests
			switch method {
			case "none":
				if request.Get("client_secret") != "" || request.Get("_authorization") != "" {
					t.Fatal("public client sent secret")
				}
			case "client_secret_post":
				if request.Get("client_secret") != r.ClientSecret || request.Get("_authorization") != "" {
					t.Fatal("post authentication")
				}
			case "client_secret_basic":
				want := "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(r.ClientID)+":"+url.QueryEscape(r.ClientSecret)))
				if request.Get("_authorization") != want || request.Get("client_secret") != "" {
					t.Fatal("basic authentication encoding")
				}
			}
		})
	}
}

func TestInvalidCallbacksNeverExchange(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	s := f.discover(t)
	a, _, callback := beginTest(t, s, "none")
	for _, mutate := range []func(*url.URL){
		func(u *url.URL) { q := u.Query(); q.Set("state", "wrong"); u.RawQuery = q.Encode() },
		func(u *url.URL) { q := u.Query(); q.Set("iss", "https://wrong.example"); u.RawQuery = q.Encode() },
		func(u *url.URL) { q := u.Query(); q.Del("iss"); u.RawQuery = q.Encode() },
		func(u *url.URL) { u.Host = "127.0.0.1:1" },
		func(u *url.URL) { u.Path = "/wrong" },
		func(u *url.URL) { u.Fragment = "unexpected" },
		func(u *url.URL) { u.RawQuery += "&code=second" },
	} {
		u, _ := url.Parse(callback)
		mutate(u)
		if _, err := f.client.Complete(context.Background(), a, u.String()); !errors.Is(err, ErrCallback) {
			t.Fatal("invalid callback accepted", err)
		}
	}
	if f.tokens.Load() != 0 {
		t.Fatal("invalid callback dispatched")
	}
	// Concurrent valid callbacks can exchange this authorization code only once.
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := f.client.Complete(context.Background(), a, callback)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrUsed) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 || f.tokens.Load() != 1 {
		t.Fatal("code exchange was duplicated")
	}
}

func TestTokenExchangeFailureDoesNotRetryOrLeak(t *testing.T) {
	t.Parallel()
	for _, status := range []int{302, 400, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newOAuthFixture(t)
			var redirectHits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectHits.Add(1) }))
			defer target.Close()
			f.tokenReply = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", target.URL+"/?secret=hidden")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error_description":"access-secret one-use-code"}`))
			}
			a, _, callback := beginTest(t, f.discover(t), "none")
			_, err := f.client.Complete(context.Background(), a, callback)
			var got *HTTPError
			if !errors.As(err, &got) || got.StatusCode != status || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "one-use-code") {
				t.Fatal("unsafe error", err)
			}
			if _, err := f.client.Complete(context.Background(), a, callback); !errors.Is(err, ErrUsed) {
				t.Fatal("failed exchange retried", err)
			}
			if redirectHits.Load() != 0 || f.tokens.Load() != 1 {
				t.Fatal("redirect or replay")
			}
		})
	}
}

func TestRefreshBindingChangeDoesNotSendCredential(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	s := f.discover(t)
	a, r, callback := beginTest(t, s, "none")
	token, err := f.client.Complete(context.Background(), a, callback)
	if err != nil {
		t.Fatal(err)
	}
	f.metadata = func(m map[string]any) { m["token_endpoint"] = f.url + "/new-token-endpoint" }
	changed := f.discover(t)
	if _, err := f.client.Refresh(context.Background(), changed, r, token); !errors.Is(err, ErrConfig) {
		t.Fatal("token endpoint changed", err)
	}
	for _, mutate := range []func(*Token){func(v *Token) { v.Binding.Resource += "/other" }, func(v *Token) { v.Binding.Issuer += "/other" }, func(v *Token) { v.ClientID = "other" }} {
		v := token
		mutate(&v)
		if _, err := f.client.Refresh(context.Background(), s, r, v); !errors.Is(err, ErrConfig) {
			t.Fatal("foreign token accepted", err)
		}
	}
	if f.tokens.Load() != 1 {
		t.Fatal("invalid refresh sent credentials")
	}
}

func TestAuthorizationDenialConsumesAttempt(t *testing.T) {
	t.Parallel()
	f := newOAuthFixture(t)
	a, _, callback := beginTest(t, f.discover(t), "none")
	u, _ := url.Parse(callback)
	q := u.Query()
	q.Del("code")
	q.Set("error", "access_denied")
	q.Set("error_description", "secret text")
	u.RawQuery = q.Encode()
	if _, err := f.client.Complete(context.Background(), a, u.String()); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := f.client.Complete(context.Background(), a, callback); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
	if f.tokens.Load() != 0 {
		t.Fatal("denied authorization exchanged")
	}
}
