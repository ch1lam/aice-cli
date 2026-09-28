// Package mcpauth implements explicit HTTP MCP OAuth exchanges. It does not
// open browsers, persist credentials, grant tool permission, or replay MCP
// requests. The application owns those decisions and the credential lock.
package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrConfig    = errors.New("invalid MCP OAuth configuration")
	ErrMetadata  = errors.New("invalid or unsupported MCP OAuth metadata")
	ErrTransport = errors.New("MCP OAuth request failed; remote details omitted")
	ErrLimit     = errors.New("MCP OAuth response exceeded its limit")
	ErrCallback  = errors.New("invalid MCP OAuth callback")
	ErrDenied    = errors.New("MCP OAuth authorization was denied")
	ErrUsed      = errors.New("MCP OAuth authorization attempt already exchanged")
)

// HTTPError never retains a remote URL, error description, or response body.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("MCP OAuth HTTP request failed (status %d)", e.StatusCode)
}
func (e *HTTPError) Unwrap() error { return ErrTransport }

const maxResponseBytes = 1 << 20

// Client's zero value is usable. An injected client must be configured before
// use. Its redirect and cookie policies are deliberately not inherited.
type Client struct{ HTTPClient *http.Client }

func validURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Hostname() == "" || u.User != nil || strings.ContainsAny(raw, "#\r\n\x00") {
		return nil, false
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, false
	}
	return u, true
}

// Loopback HTTP supports local services and fixtures, but HTTPS discovery must
// never downgrade authorization or credential exchange to an HTTP endpoint.
func endpointFor(origin, endpoint string) bool {
	base, ok := validURL(origin)
	if !ok {
		return false
	}
	u, ok := validURL(endpoint)
	return ok && (base.Scheme != "https" || u.Scheme == "https")
}

func canonicalResource(raw string) (string, error) {
	u, ok := validURL(raw)
	if !ok {
		return "", ErrConfig
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

func (c Client) request(ctx context.Context, method, endpoint, contentType string, body []byte, basic *Registration) (*http.Response, context.CancelFunc, error) {
	if _, ok := validURL(endpoint); !ok {
		return nil, nil, ErrConfig
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, nil, ErrConfig
	}
	req.GetBody = nil // no net/http replay of a code or rotating refresh token
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if basic != nil {
		req.SetBasicAuth(url.QueryEscape(basic.ClientID), url.QueryEscape(basic.ClientSecret))
	}
	client := http.Client{}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		cause := ctx.Err()
		cancel()
		if cause != nil {
			return nil, nil, cause
		}
		return nil, nil, ErrTransport
	}
	return resp, cancel, nil
}

func (c Client) exchangeJSON(ctx context.Context, method, endpoint, contentType string, body []byte, basic *Registration, out any) error {
	resp, cancel, err := c.request(ctx, method, endpoint, contentType, body, basic)
	if err != nil {
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && !(method == http.MethodPost && resp.StatusCode == http.StatusCreated) {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if resp.Request != nil && resp.Request.Context().Err() != nil {
			return resp.Request.Context().Err()
		}
		return ErrTransport
	}
	if len(data) > maxResponseBytes {
		return ErrLimit
	}
	if !utf8.Valid(data) || !uniqueObject(data) || json.Unmarshal(data, out) != nil {
		return ErrMetadata
	}
	return nil
}

// Security-relevant metadata and token fields are top-level. Reject duplicate
// keys instead of accepting whichever issuer/token happened to appear last.
func uniqueObject(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool)
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		key = strings.ToLower(key) // encoding/json also accepts case-insensitive field aliases
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return false
		}
	}
	_, err = d.Token()
	return err == nil && d.Decode(new(json.RawMessage)) == io.EOF
}

func boundedValue(value string, required bool) bool {
	if required && strings.TrimSpace(value) == "" {
		return false
	}
	if len(value) > 8192 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validScopes(scopes []string) bool {
	if len(scopes) > 128 || len(strings.Join(scopes, " ")) > 8192 {
		return false
	}
	for _, s := range scopes {
		if s == "" {
			return false
		}
		for _, r := range s {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return false
			}
		}
	}
	return true
}
