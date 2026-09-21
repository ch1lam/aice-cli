// Package exa adapts the Exa Search REST API (POST /search) to the
// provider-neutral web.SearchBackend contract. Exa wire types stop here.
package exa

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Descriptor facts for the Exa provider. The application composes these into
// its fixed factory list; there is no mutable registry.
const (
	ProviderID     = "exa"
	APIID          = "exa-rest"
	DefaultBaseURL = "https://api.exa.ai"
	Label          = "Exa"

	// Search modes accepted by this adapter. Both are documented request
	// values for the "type" field; deep variants synthesize answers and are
	// rejected explicitly rather than mapped to auto.
	SearchTypeAuto = "auto"
	SearchTypeFast = "fast"

	maxSuccessBodyBytes = 2 * 1024 * 1024
	maxErrorBodyBytes   = 8 * 1024
	maxHighlightBytes   = 2 * 1024
	maxDocumentBytes    = 8 * 1024
	maxSummaryBytes     = 2 * 1024
	maxUpstreamResults  = 100
)

// Options are the user-configurable request options for one instance. Unknown
// fields are configuration errors so a misspelling cannot be silently ignored.
type Options struct {
	Type string `json:"type,omitempty"`
}

// ParseOptions decodes and validates instance options.
func ParseOptions(raw json.RawMessage) (Options, error) {
	options := Options{Type: SearchTypeAuto}
	if len(raw) == 0 {
		return options, nil
	}
	if err := jsonutil.DecodeStrict(raw, &options); err != nil {
		return Options{}, fmt.Errorf("options: %w", err)
	}
	options.Type = strings.TrimSpace(options.Type)
	switch options.Type {
	case "":
		options.Type = SearchTypeAuto
	case SearchTypeAuto, SearchTypeFast:
	case "deep", "deep-lite", "deep-reasoning", "neural", "keyword":
		return Options{}, fmt.Errorf("options.type %q is not supported by this version; use %q or %q", options.Type, SearchTypeAuto, SearchTypeFast)
	default:
		return Options{}, fmt.Errorf("options.type %q is unknown; use %q or %q", options.Type, SearchTypeAuto, SearchTypeFast)
	}
	return options, nil
}

// Config constructs one client. The application supplies the resolved secret;
// this package never reads settings files or the environment.
type Config struct {
	InstanceID string
	BaseURL    string
	APIKey     string
	Options    json.RawMessage
	Timeout    time.Duration
	// Transport overrides the HTTP transport, for tests. Nil uses a dedicated
	// transport with standard proxy environment handling for API traffic.
	Transport http.RoundTripper
	// Now supplies retrieval timestamps; nil uses time.Now.
	Now func() time.Time
}

// ResolveBaseURL applies the descriptor default and validates the origin.
func ResolveBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultBaseURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return nil, web.NewError(web.CodeInvalidConfig, "base_url must be an absolute URL with a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, web.NewError(web.CodeInvalidConfig, "base_url must not contain userinfo, query or fragment")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
		if host != "localhost" && host != "::1" && !strings.HasPrefix(host, "127.") {
			return nil, web.NewError(web.CodeInvalidConfig, "base_url may use http only for loopback development gateways")
		}
	default:
		return nil, web.NewError(web.CodeInvalidConfig, "base_url must use https")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed, nil
}

// Origin returns scheme://host[:port] for permission fingerprints and display.
func Origin(base *url.URL) string {
	return strings.ToLower(base.Scheme) + "://" + strings.ToLower(base.Host)
}
