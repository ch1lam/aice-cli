package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ResolveServiceBaseURL applies a service's default endpoint and validates
// the configured origin: HTTPS with an optional path prefix, or HTTP for a
// loopback development gateway. Userinfo, query and fragment are rejected.
func ResolveServiceBaseURL(raw, fallback string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fallback
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return nil, NewError(CodeInvalidConfig, "base_url must be an absolute URL with a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, NewError(CodeInvalidConfig, "base_url must not contain userinfo, query or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		if !IsLoopbackHost(parsed.Hostname()) {
			return nil, NewError(CodeInvalidConfig, "base_url may use http only for loopback development gateways")
		}
	default:
		return nil, NewError(CodeInvalidConfig, "base_url must use https")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed, nil
}

// ServiceOrigin returns scheme://host[:port] for permission fingerprints and display.
func ServiceOrigin(base *url.URL) string {
	return strings.ToLower(base.Scheme) + "://" + strings.ToLower(base.Host)
}

// NewServiceHTTPClient builds the credentialed client of one search service.
// It never follows redirects, so a moved endpoint cannot receive the key on
// another origin. A nil transport uses a dedicated one with the standard
// proxy environment.
func NewServiceHTTPClient(timeout time.Duration, transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       60 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// CloseIdle releases a service client's idle connections.
func CloseIdle(client *http.Client) {
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

// ReadBounded reads at most limit bytes and fails when more are available,
// independently of Content-Length and chunked encoding.
func ReadBounded(body io.Reader, limit int) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, WrapContextError(err, CodeUnavailable, "read search response")
		}
		return nil, WrapContextError(err, CodeUnavailable, "read search response failed")
	}
	if len(payload) > limit {
		return nil, NewError(CodeResponseTooLarge, "search response exceeds %d bytes", limit)
	}
	return payload, nil
}

// StatusError classifies a non-success search service status. message is
// the upstream explanation, already extracted; it is sanitized here.
func StatusError(response *http.Response, message string) *Error {
	status := response.StatusCode
	if message == "" {
		message = http.StatusText(status)
	}
	code := CodeInvalidResponse
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = CodeAuthentication
	case status == http.StatusPaymentRequired:
		code = CodeQuotaExceeded
	case status == http.StatusTooManyRequests:
		code = CodeRateLimited
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		code = CodeInvalidArgument
	case status == http.StatusRequestTimeout || status >= 500:
		code = CodeUnavailable
	}
	return &Error{
		Code:       code,
		Message:    fmt.Sprintf("search service returned HTTP %d: %s", status, Sanitize(message)),
		RetryAfter: response.Header.Get("Retry-After"),
	}
}
