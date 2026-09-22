package web

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/evidence"
)

// MaxURLBytes bounds the model-supplied URL before parsing.
const MaxURLBytes = 8192

// FetchFormat selects the model-facing rendering of fetched content.
type FetchFormat string

const (
	FetchFormatMarkdown FetchFormat = "markdown"
	FetchFormatText     FetchFormat = "text"
)

// FetchRequest is the provider-neutral input to one page fetch. No headers,
// cookies, proxies or safety switches are model parameters.
type FetchRequest struct {
	URL    string
	Format FetchFormat
}

// Validate checks the model-controlled fields. Address policy belongs to the
// fetch backend, which sees the parsed URL.
func (r FetchRequest) Validate() error {
	raw := strings.TrimSpace(r.URL)
	if raw == "" {
		return NewError(CodeInvalidArgument, "url is required")
	}
	if !utf8.ValidString(raw) {
		return NewError(CodeInvalidArgument, "url must be valid UTF-8")
	}
	if len(raw) > MaxURLBytes {
		return NewError(CodeInvalidArgument, "url exceeds %d bytes", MaxURLBytes)
	}
	switch r.Format {
	case "", FetchFormatMarkdown, FetchFormatText:
	default:
		return NewError(CodeInvalidArgument, "format must be markdown or text")
	}
	return nil
}

// FetchTarget is a validated, normalized fetch URL and its origin.
type FetchTarget struct {
	URL    string
	Host   string
	Port   int
	Origin string
}

// ValidateFetchURL applies the URL-shape policy shared by the fetcher and the
// execution gate: absolute http(s), no userinfo, no zone-scoped IPv6 literal,
// default web port only. Address policy is applied later by the fetcher.
func ValidateFetchURL(raw string) (FetchTarget, error) {
	if err := (FetchRequest{URL: raw}).Validate(); err != nil {
		return FetchTarget{}, err
	}
	normalized, err := evidence.NormalizeURL(raw)
	if err != nil {
		return FetchTarget{}, NewError(CodeInvalidArgument, "%v", err)
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return FetchTarget{}, NewError(CodeInvalidArgument, "parse url: %v", err)
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, "%") {
		return FetchTarget{}, NewError(CodeBlockedTarget, "zone-scoped IPv6 literals are not allowed")
	}
	port := 80
	if parsed.Scheme == "https" {
		port = 443
	}
	if explicit := parsed.Port(); explicit != "" {
		value, err := strconv.Atoi(explicit)
		if err != nil || value <= 0 || value > 65535 {
			return FetchTarget{}, NewError(CodeInvalidArgument, "invalid port %q", explicit)
		}
		port = value
	}
	if (parsed.Scheme == "http" && port != 80) || (parsed.Scheme == "https" && port != 443) {
		return FetchTarget{}, NewError(CodeBlockedTarget, "port %d is not supported; only the default web ports (80 for http, 443 for https) are allowed", port)
	}
	displayHost := host
	if strings.Contains(host, ":") {
		displayHost = "[" + host + "]"
	}
	return FetchTarget{URL: normalized, Host: host, Port: port, Origin: parsed.Scheme + "://" + displayHost}, nil
}

// FetchBackend is the consumer contract implemented by the HTTP fetcher.
type FetchBackend interface {
	Fetch(context.Context, FetchRequest) (FetchResponse, error)
}

// FetchResponse is the normalized result of one fetch.
type FetchResponse struct {
	RequestedURL string
	FinalURL     string
	HTTPStatus   int
	MediaType    string
	// Extraction names the body selection method (main, article, body, text).
	Extraction string
	Evidence   evidence.Bundle
}
