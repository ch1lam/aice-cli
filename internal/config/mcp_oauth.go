package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

const mcpOAuthKey = "mcp_oauth"

// MCPOAuthSettings describes a public dynamic client when ClientID is empty.
// Confidential clients reference a scoped secret; settings never hold it.
type MCPOAuthSettings struct {
	Issuer       string       `json:"issuer,omitempty"`
	ClientID     string       `json:"client_id,omitempty"`
	ClientSecret *MCPValueRef `json:"client_secret,omitempty"`
	AuthMethod   string       `json:"token_endpoint_auth_method,omitempty"`
	RedirectURI  string       `json:"redirect_uri,omitempty"`
	Scopes       *[]string    `json:"scopes,omitempty"`
}

func (s MCPOAuthSettings) Method() string {
	if s.AuthMethod == "" {
		return "none"
	}
	return s.AuthMethod
}

func (s MCPOAuthSettings) valid() bool {
	if !mcpText(s.ClientID, 8192) || s.Issuer != "" && !validMCPOAuthIssuer(s.Issuer) || s.Scopes != nil && !validMCPOAuthScopes(*s.Scopes) {
		return false
	}
	if s.RedirectURI != "" && !validMCPOAuthRedirect(s.RedirectURI) {
		return false
	}
	if s.ClientID == "" && (s.ClientSecret != nil || s.Method() != "none") {
		return false
	}
	if s.ClientID != "" && s.RedirectURI == "" {
		return false
	}
	switch s.Method() {
	case "none":
		return s.ClientSecret == nil
	case "client_secret_basic", "client_secret_post":
		return s.ClientSecret != nil && s.ClientSecret.valid(false) && s.ClientSecret.Prefix == ""
	default:
		return false
	}
}

func (s MCPOAuthSettings) clone() MCPOAuthSettings {
	if s.ClientSecret != nil {
		ref := *s.ClientSecret
		if ref.Value != nil {
			v := *ref.Value
			ref.Value = &v
		}
		s.ClientSecret = &ref
	}
	if s.Scopes != nil {
		scopes := append([]string{}, (*s.Scopes)...)
		s.Scopes = &scopes
	}
	return s
}

// MCPOAuthCredentials is a user-auth-file record, never a settings/Session
// value. GrantID identifies one login and stays stable only during its refreshes.
type MCPOAuthCredentials struct {
	GrantID       string    `json:"grant_id"`
	Resource      string    `json:"resource"`
	Issuer        string    `json:"issuer"`
	TokenEndpoint string    `json:"token_endpoint"`
	ClientID      string    `json:"client_id"`
	ClientSecret  string    `json:"client_secret,omitempty"`
	AuthMethod    string    `json:"token_endpoint_auth_method"`
	RedirectURI   string    `json:"redirect_uri"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
}

type mcpOAuthIdentity struct{ Grant, Resource, Issuer, TokenEndpoint, ClientID, ClientSecret, AuthMethod, RedirectURI string }

func (c MCPOAuthCredentials) identity() mcpOAuthIdentity {
	return mcpOAuthIdentity{c.GrantID, c.Resource, c.Issuer, c.TokenEndpoint, c.ClientID, c.ClientSecret, c.AuthMethod, c.RedirectURI}
}

func (MCPOAuthCredentials) String() string               { return "MCP OAuth credentials (values omitted)" }
func (c MCPOAuthCredentials) clone() MCPOAuthCredentials { c.Scopes = slices.Clone(c.Scopes); return c }

func (c MCPOAuthCredentials) valid() bool {
	if !mcpHexDigest(c.GrantID) || !validMCPOAuthURL(c.Resource) || !validMCPOAuthIssuer(c.Issuer) || !validMCPOAuthURL(c.TokenEndpoint) || !validMCPOAuthRedirect(c.RedirectURI) || !validMCPOAuthScopes(c.Scopes) {
		return false
	}
	for _, value := range []string{c.ClientID, c.ClientSecret, c.AccessToken, c.RefreshToken} {
		if !mcpText(value, 8192) {
			return false
		}
		for _, r := range value {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	if strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.AccessToken) == "" || strings.Contains(c.AccessToken, " ") {
		return false
	}
	resource, _ := url.Parse(c.Resource)
	issuer, _ := url.Parse(c.Issuer)
	token, _ := url.Parse(c.TokenEndpoint)
	if resource.Scheme == "https" && issuer.Scheme != "https" || issuer.Scheme == "https" && token.Scheme != "https" {
		return false
	}
	switch c.AuthMethod {
	case "none":
		return c.ClientSecret == ""
	case "client_secret_basic", "client_secret_post":
		return c.ClientSecret != ""
	default:
		return false
	}
}

func mcpHexDigest(s string) bool {
	value, err := hex.DecodeString(s)
	return err == nil && len(value) == 32 && strings.ToLower(s) == s
}
func validMCPOAuthURL(s string) bool { return validMCPEndpoint(s) && !strings.Contains(s, "#") }
func validMCPOAuthIssuer(s string) bool {
	u, err := url.Parse(s)
	return err == nil && validMCPOAuthURL(s) && u.RawQuery == "" && !u.ForceQuery
}
func validMCPOAuthRedirect(s string) bool {
	u, err := url.Parse(s)
	if err != nil || !validMCPOAuthURL(s) || u.Scheme != "http" || u.RawQuery != "" || u.ForceQuery || u.Path == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	return ip != nil && ip.IsLoopback() && err == nil && port > 0 && port <= 65535
}
func validMCPOAuthScopes(scopes []string) bool {
	if len(scopes) > 128 || len(strings.Join(scopes, " ")) > 8192 {
		return false
	}
	for _, scope := range scopes {
		if scope == "" {
			return false
		}
		for _, r := range scope {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return false
			}
		}
	}
	return true
}
func mcpOAuthResource(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
		u.RawPath = ""
	}
	return u.String()
}

type mcpOAuthCredentials map[string]map[string]MCPOAuthCredentials

func decodeMCPOAuth(raw any) (mcpOAuthCredentials, error) {
	data, err := json.Marshal(raw)
	var records mcpOAuthCredentials
	if err != nil || len(data) > maxMCPSettingsBytes || jsonutil.DecodeStrict(data, &records) != nil || records == nil || len(records) > 1024 {
		return nil, fmt.Errorf("config: invalid mcp_oauth credentials (values omitted)")
	}
	for key, scopes := range records {
		if key == "" || !mcpText(key, 256) || scopes == nil {
			return nil, fmt.Errorf("config: invalid MCP OAuth credential binding")
		}
		for scope, credential := range scopes {
			if !mcpHexDigest(scope) || !credential.valid() {
				return nil, fmt.Errorf("config: invalid MCP OAuth credential record (values omitted)")
			}
		}
	}
	return records, nil
}
func cloneMCPOAuth(records mcpOAuthCredentials) mcpOAuthCredentials {
	copy := make(mcpOAuthCredentials, len(records))
	for key, scopes := range records {
		copy[key] = make(map[string]MCPOAuthCredentials, len(scopes))
		for scope, c := range scopes {
			copy[key][scope] = c.clone()
		}
	}
	return copy
}

func (s MCPServer) acceptsOAuth(c MCPOAuthCredentials) bool {
	o := s.Settings.OAuth
	if o == nil || !c.valid() || c.Resource != mcpOAuthResource(s.Settings.URL) || o.Issuer != "" && c.Issuer != o.Issuer {
		return false
	}
	if o.ClientID != "" {
		return c.ClientID == o.ClientID && c.AuthMethod == o.Method() && c.ClientSecret == s.oauthClientSecret && c.RedirectURI == o.RedirectURI
	}
	return c.AuthMethod == "none" && (o.RedirectURI == "" || c.RedirectURI == o.RedirectURI)
}

func (s *MCPServer) applyOAuth(c MCPOAuthCredentials) {
	if s.Settings.OAuth == nil {
		return
	}
	if s.acceptsOAuth(c) {
		credential := c.clone()
		s.oauthCredential = &credential
	} else {
		s.MissingValues = append(s.MissingValues, "oauth")
	}
	slices.Sort(s.MissingValues)
	s.Fingerprint = s.connectionFingerprint()
}

// OAuthCredentials returns a private copy for the application-owned login and
// transport preparation path. Ordinary configuration serialization omits it.
func (s MCPServer) OAuthCredentials() (MCPOAuthCredentials, bool) {
	if s.oauthCredential == nil {
		return MCPOAuthCredentials{}, false
	}
	return s.oauthCredential.clone(), true
}
func (s MCPServer) OAuthClientSecret() string { return s.oauthClientSecret }

// OAuthTokenExpired checks the frozen token without I/O. An expired token must
// be refreshed and durably published before a connection can use it again.
func (s MCPServer) OAuthTokenExpired(now time.Time) bool {
	return s.oauthCredential != nil && !s.oauthCredential.ExpiresAt.IsZero() && !s.oauthCredential.ExpiresAt.After(now)
}

func (s MCPServer) connectionFingerprint() string {
	base := mcpDigest(struct {
		Scope                string
		Environment, Headers map[string]string
	}{s.CredentialScope, s.environment, s.headers})
	if s.Settings.OAuth == nil {
		return base
	} // preserve every pre-OAuth identity
	grant := ""
	if s.oauthCredential != nil {
		grant = mcpDigest(s.oauthCredential.identity())
	}
	return mcpDigest(struct{ Connection, ClientSecret, Grant string }{base, s.oauthClientSecret, grant})
}

func (c Config) WithMCPOAuth(key, scope string, credential MCPOAuthCredentials) (Config, error) {
	server, ok := c.MCP.Servers[key]
	if !ok || scope != server.CredentialScope || !server.acceptsOAuth(credential) {
		return Config{}, fmt.Errorf("config: invalid MCP OAuth credential binding")
	}
	next := c
	next.mcpInputs.oauth = cloneMCPOAuth(c.mcpInputs.oauth)
	if next.mcpInputs.oauth[key] == nil {
		next.mcpInputs.oauth[key] = make(map[string]MCPOAuthCredentials)
	}
	next.mcpInputs.oauth[key][scope] = credential.clone()
	if _, err := decodeMCPOAuth(next.mcpInputs.oauth); err != nil {
		return Config{}, err
	}
	var err error
	next.MCP, err = effectiveMCP(next.mcpInputs, c.Paths, c.environmentLookup)
	return next, err
}

func (c Config) WithoutMCPOAuth(key string) (Config, error) {
	if _, ok := c.MCP.Servers[key]; !ok {
		return Config{}, fmt.Errorf("config: MCP service is unavailable")
	}
	next := c
	next.mcpInputs.oauth = cloneMCPOAuth(c.mcpInputs.oauth)
	next.mcpInputs.connections = maps.Clone(c.mcpInputs.connections)
	next.mcpInputs.permissions = cloneMCPPermissions(c.mcpInputs.permissions)
	delete(next.mcpInputs.permissions, key)
	delete(next.mcpInputs.oauth, key)
	delete(next.mcpInputs.connections, key)
	var err error
	next.MCP, err = effectiveMCP(next.mcpInputs, c.Paths, c.environmentLookup)
	return next, err
}

// WithRefreshedMCPOAuth updates a transport snapshot without changing its grant
// identity or loading configuration. The caller must persist the token first.
func (s MCPServer) WithRefreshedMCPOAuth(c MCPOAuthCredentials) (MCPServer, error) {
	previous, ok := s.OAuthCredentials()
	if !ok || !sameMCPOAuthGrant(previous, c) || !s.acceptsOAuth(c) {
		return MCPServer{}, fmt.Errorf("config: MCP OAuth refresh changed the login identity")
	}
	credential := c.clone()
	s.oauthCredential = &credential
	return s, nil
}
