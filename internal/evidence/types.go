// Package evidence defines the minimal provider-neutral source and evidence
// contract shared by web tools, transcript messages and their display. It has
// no dependency on configuration, network clients, providers, the Agent Loop,
// the TUI or Session storage so that llm can retain it as message metadata.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// Kind identifies the nature of one evidence item. Kinds are never
// interchangeable: a search snippet, a source excerpt, an extracted document
// and a generated summary carry different authority.
type Kind string

const (
	KindSnippet  Kind = "snippet"
	KindExcerpt  Kind = "excerpt"
	KindDocument Kind = "document"
	KindSummary  Kind = "summary"
)

// Format identifies the text format of one evidence item.
type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

// Acquisition identifies how AICE obtained the evidence.
type Acquisition string

const (
	AcquisitionSearchService Acquisition = "search_service"
	AcquisitionHTTPFetch     Acquisition = "http_fetch"
)

// Source is one external location evidence was obtained from. PublishedAt is
// filled only when the upstream supplied a date that parsed successfully; the
// retrieval time lives on the Evidence items.
type Source struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

// Evidence is one bounded piece of text obtained from a Source.
type Evidence struct {
	SourceID      string      `json:"source_id"`
	Kind          Kind        `json:"kind"`
	Text          string      `json:"text"`
	Format        Format      `json:"format"`
	Acquisition   Acquisition `json:"acquisition"`
	RetrievedAt   int64       `json:"retrieved_at"`
	Truncated     bool        `json:"truncated,omitempty"`
	ReturnedBytes int         `json:"returned_bytes"`
}

// Cost is an upstream-reported charge. Absent means unknown, never zero.
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Source   string  `json:"source"`
}

// Diagnostics are operational details that must not enter model-facing text.
type Diagnostics struct {
	UpstreamRequestID string   `json:"upstream_request_id,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	DurationMS        int64    `json:"duration_ms,omitempty"`
	BytesReceived     int64    `json:"bytes_received,omitempty"`
	ReportedCost      *Cost    `json:"reported_cost,omitempty"`
}

// Bundle is the complete set of sources and evidence produced by one tool
// call. Sources are unique by ID; every evidence item references a source.
type Bundle struct {
	Sources     []Source    `json:"sources"`
	Items       []Evidence  `json:"items"`
	Diagnostics Diagnostics `json:"diagnostics,omitzero"`
}

// MaxBundleBytes bounds the encoded metadata retained in Session history.
const MaxBundleBytes = 64 * 1024

// NormalizeURL applies only safe canonicalization: lowercase scheme and host,
// drop a default port and an empty fragment marker. Paths, queries and
// non-empty fragments remain untouched because they can change the content.
// Only absolute http(s) URLs without userinfo are actionable sources.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("evidence: url is empty")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return "", fmt.Errorf("evidence: url contains whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("evidence: parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("evidence: unsupported url scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("evidence: url must not contain userinfo")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("evidence: url has no host")
	}
	if isIPLiteral(host) {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	} else if ascii, err := idna.Lookup.ToASCII(host); err == nil && ascii != "" {
		host = ascii
	} else {
		return "", fmt.Errorf("evidence: url host %q is not a valid hostname", host)
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	parsed.Scheme = scheme
	if port == "" {
		parsed.Host = host
	} else {
		parsed.Host = host + ":" + port
	}
	if parsed.Fragment == "" {
		parsed.RawFragment = ""
	}
	return parsed.String(), nil
}

func isIPLiteral(host string) bool {
	_, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil
}

// SourceID derives a deterministic identifier from the normalized URL so the
// same source yields the same ID across calls, processes and Sessions.
func SourceID(normalizedURL string) string {
	sum := sha256.Sum256([]byte(normalizedURL))
	return hex.EncodeToString(sum[:16])
}

// NewSource normalizes the URL and derives the deterministic ID. PublishedAt
// is retained only when it parses as RFC 3339 or a plain calendar date.
func NewSource(rawURL, title, publishedAt string) (Source, error) {
	normalized, err := NormalizeURL(rawURL)
	if err != nil {
		return Source{}, err
	}
	return Source{
		ID:          SourceID(normalized),
		URL:         normalized,
		Title:       strings.TrimSpace(title),
		PublishedAt: ParsePublishedAt(publishedAt),
	}, nil
}

// ParsePublishedAt returns a canonical RFC 3339 string when the input parses,
// otherwise the empty string. It never substitutes the current time.
func ParsePublishedAt(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return ""
}
