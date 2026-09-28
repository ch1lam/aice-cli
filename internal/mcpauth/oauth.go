package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Binding is persisted with credentials. Refresh must use the same resource,
// issuer and token endpoint, even if later discovery advertises new endpoints.
type Binding struct {
	Resource      string `json:"resource"`
	Issuer        string `json:"issuer"`
	TokenEndpoint string `json:"token_endpoint"`
}

type Registration struct {
	Binding      Binding `json:"binding"`
	ClientID     string  `json:"client_id"`
	ClientSecret string  `json:"client_secret,omitempty"`
	AuthMethod   string  `json:"token_endpoint_auth_method"`
	RedirectURI  string  `json:"redirect_uri"`
}

func (Registration) String() string { return "MCP OAuth client (credentials omitted)" }

type Token struct {
	Binding      Binding   `json:"binding"`
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
}

func (Token) String() string { return "MCP OAuth token (credentials omitted)" }

func (s Server) binding() Binding {
	return Binding{Resource: s.resource, Issuer: s.issuer, TokenEndpoint: s.token}
}

// PreRegistered binds user-supplied client credentials to this discovery. The
// application must keep them in this MCP service's credential namespace.
func (s Server) PreRegistered(id, secret, method, redirect string) (Registration, error) {
	r := Registration{Binding: s.binding(), ClientID: id, ClientSecret: secret, AuthMethod: method, RedirectURI: redirect}
	if !s.validRegistration(r) {
		return Registration{}, ErrConfig
	}
	return r, nil
}

func validRedirect(raw string) bool {
	u, ok := validURL(raw)
	if !ok || u.Scheme != "http" || u.RawQuery != "" || u.ForceQuery || u.Path == "" || u.Port() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	return ip != nil && ip.IsLoopback() && err == nil && port > 0 && port <= 65535 // literal loopback; no DNS or wildcard listener
}

func (s Server) validRegistration(r Registration) bool {
	if s.resource == "" || r.Binding != s.binding() || !boundedValue(r.ClientID, true) || !boundedValue(r.ClientSecret, false) || !validRedirect(r.RedirectURI) || !slices.Contains(s.authMethods, r.AuthMethod) {
		return false
	}
	switch r.AuthMethod {
	case "none":
		return r.ClientSecret == ""
	case "client_secret_basic", "client_secret_post":
		return r.ClientSecret != ""
	default:
		return false
	}
}

// Register creates a public native client only. It is an explicit management
// action, never a discovery side effect. There is no registration retry.
func (c Client) Register(ctx context.Context, s Server, redirect string) (Registration, error) {
	if s.registration == "" || !validRedirect(redirect) || !slices.Contains(s.authMethods, "none") {
		return Registration{}, ErrConfig
	}
	body, _ := json.Marshal(struct {
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
		Grants    []string `json:"grant_types"`
		Responses []string `json:"response_types"`
		Method    string   `json:"token_endpoint_auth_method"`
	}{"AICE", []string{redirect}, []string{"authorization_code", "refresh_token"}, []string{"code"}, "none"})
	var reply struct {
		ID        string   `json:"client_id"`
		Secret    string   `json:"client_secret"`
		Method    string   `json:"token_endpoint_auth_method"`
		Redirects []string `json:"redirect_uris"`
		Error     string   `json:"error"`
	}
	if err := c.exchangeJSON(ctx, http.MethodPost, s.registration, "application/json", body, nil, &reply); err != nil {
		return Registration{}, err
	}
	if reply.Error != "" || reply.Secret != "" || reply.Method != "none" || len(reply.Redirects) != 1 || reply.Redirects[0] != redirect {
		return Registration{}, ErrMetadata
	}
	r, err := s.PreRegistered(reply.ID, "", "none", redirect)
	if err != nil {
		return Registration{}, ErrMetadata
	}
	return r, nil
}

// Attempt is one in-memory authorization-code exchange. The caller owns the
// listener/browser and sends the exact received callback URL to Complete.
// Invalid callbacks do not consume the attempt; a valid code does, even when
// the token request fails or is canceled. Do not copy an Attempt.
type Attempt struct {
	server                            Server
	registration                      Registration
	state, verifier, authorizationURL string
	used                              atomic.Bool
}

func (*Attempt) String() string             { return "MCP OAuth attempt (authorization details omitted)" }
func (a *Attempt) AuthorizationURL() string { return a.authorizationURL }

func (s Server) Begin(r Registration) (*Attempt, error) {
	if !s.validRegistration(r) {
		return nil, ErrConfig
	}
	verifierBytes, stateBytes := make([]byte, 32), make([]byte, 32)
	_, _ = rand.Read(verifierBytes)
	_, _ = rand.Read(stateBytes)
	a := &Attempt{server: s, registration: r, state: base64.RawURLEncoding.EncodeToString(stateBytes), verifier: base64.RawURLEncoding.EncodeToString(verifierBytes)}
	u, _ := url.Parse(s.authorization)
	query := u.Query()
	// Replace protocol parameters rather than appending duplicates from metadata.
	for _, key := range []string{"client_id", "client_secret", "response_type", "redirect_uri", "state", "code_challenge", "code_challenge_method", "resource", "scope"} {
		query.Del(key)
	}
	query.Set("client_id", r.ClientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", r.RedirectURI)
	query.Set("state", a.state)
	challenge := sha256.Sum256([]byte(a.verifier))
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	query.Set("resource", s.resource)
	if len(s.scopes) != 0 {
		query.Set("scope", strings.Join(s.scopes, " "))
	}
	u.RawQuery = query.Encode()
	a.authorizationURL = u.String()
	return a, nil
}

func (c Client) Complete(ctx context.Context, a *Attempt, callback string) (Token, error) {
	if a == nil {
		return Token{}, ErrCallback
	}
	u, ok := validURL(callback)
	redirect, _ := url.Parse(a.registration.RedirectURI)
	if !ok || u.Scheme != redirect.Scheme || u.Host != redirect.Host || u.EscapedPath() != redirect.EscapedPath() {
		return Token{}, ErrCallback
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Token{}, ErrCallback
	}
	for _, values := range q {
		if len(values) != 1 {
			return Token{}, ErrCallback
		}
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(a.state)) != 1 {
		return Token{}, ErrCallback
	}
	issuer := q.Get("iss")
	if (a.server.responseIssuer || issuer != "") && issuer != a.server.issuer {
		return Token{}, ErrCallback
	}
	if q.Get("error") != "" {
		if !a.used.CompareAndSwap(false, true) {
			return Token{}, ErrUsed
		}
		return Token{}, ErrDenied
	}
	code := q.Get("code")
	if !boundedValue(code, true) {
		return Token{}, ErrCallback
	}
	if !a.used.CompareAndSwap(false, true) {
		return Token{}, ErrUsed
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {a.registration.RedirectURI}, "code_verifier": {a.verifier}}
	return c.token(ctx, a.server, a.registration, form, Token{Scopes: a.server.Scopes()})
}

// Refresh exchanges exactly once. The application serializes refresh and
// persistence under its credential lock and never replays a failed MCP call.
func (c Client) Refresh(ctx context.Context, s Server, r Registration, previous Token) (Token, error) {
	if !s.validRegistration(r) || previous.Binding != s.binding() || previous.ClientID != r.ClientID || !boundedValue(previous.RefreshToken, true) || !validScopes(previous.Scopes) {
		return Token{}, ErrConfig
	}
	return c.token(ctx, s, r, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {previous.RefreshToken}}, previous)
}

func (c Client) token(ctx context.Context, s Server, r Registration, form url.Values, previous Token) (Token, error) {
	form.Set("resource", s.resource)
	form.Set("client_id", r.ClientID)
	var basic *Registration
	switch r.AuthMethod {
	case "client_secret_basic":
		basic = &r
	case "client_secret_post":
		form.Set("client_secret", r.ClientSecret)
	}
	var reply struct {
		Access  string      `json:"access_token"`
		Refresh string      `json:"refresh_token"`
		Type    string      `json:"token_type"`
		Expires json.Number `json:"expires_in"`
		Scope   *string     `json:"scope"`
		Error   string      `json:"error"`
	}
	// Expiry starts before the network exchange, conservatively accounting for
	// request latency rather than extending the token's lifetime on receipt.
	started := time.Now()
	if err := c.exchangeJSON(ctx, http.MethodPost, s.token, "application/x-www-form-urlencoded", []byte(form.Encode()), basic, &reply); err != nil {
		return Token{}, err
	}
	if reply.Error != "" || !strings.EqualFold(reply.Type, "Bearer") || !boundedValue(reply.Access, true) || strings.Contains(reply.Access, " ") || !boundedValue(reply.Refresh, false) {
		return Token{}, ErrMetadata
	}
	out := Token{Binding: s.binding(), ClientID: r.ClientID, AccessToken: reply.Access, RefreshToken: reply.Refresh, Scopes: slices.Clone(previous.Scopes)}
	if out.RefreshToken == "" {
		out.RefreshToken = previous.RefreshToken
	}
	if reply.Expires != "" {
		seconds, err := reply.Expires.Int64()
		if err != nil || seconds <= 0 || seconds > 366*24*60*60 {
			return Token{}, ErrMetadata
		}
		out.ExpiresAt = started.Add(time.Duration(seconds) * time.Second)
	}
	if reply.Scope != nil {
		out.Scopes = nil
		if *reply.Scope != "" {
			out.Scopes = strings.Split(*reply.Scope, " ")
		}
		if !validScopes(out.Scopes) {
			return Token{}, ErrMetadata
		}
	}
	return out, nil
}
