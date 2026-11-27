// Package deepseek adapts the DeepSeek Search API (POST /v1/tools/search) to
// the provider-neutral web.SearchBackend contract. DeepSeek wire types stop
// here; model-native DeepSeek search belongs to the protocol adapters.
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Descriptor facts for the DeepSeek search provider. The application
// composes these into its fixed factory list; there is no mutable registry.
const (
	ProviderID     = "deepseek"
	APIID          = "deepseek-search"
	DefaultBaseURL = "https://api.deepseek.com"
	Label          = "DeepSeek"

	// MaxQueryRunes is the documented query limit. Domain operators count
	// toward it.
	MaxQueryRunes = 128

	maxSuccessBodyBytes = 2 * 1024 * 1024
	maxErrorBodyBytes   = 8 * 1024
)

// ParseOptions accepts only an empty options object: the adapter exposes no
// request options, and a misspelling must not be silently ignored.
func ParseOptions(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(raw, &options); err != nil || options == nil {
		return fmt.Errorf("options must be a JSON object")
	}
	for key := range options {
		return fmt.Errorf("options.%s is unknown; DeepSeek search has no options", key)
	}
	return nil
}

// Config constructs one client. The application supplies the resolved secret;
// this package never reads settings files or the environment.
type Config struct {
	InstanceID string
	BaseURL    string
	APIKey     string
	Options    json.RawMessage
	Timeout    time.Duration
	// Transport overrides the HTTP transport, for tests.
	Transport http.RoundTripper
	// Now supplies retrieval timestamps; nil uses time.Now.
	Now func() time.Time
}

// Client is one configured DeepSeek search instance, safe for concurrent use.
type Client struct {
	instanceID string
	base       *url.URL
	apiKey     string
	timeout    time.Duration
	http       *http.Client
	now        func() time.Time
}

// New validates the instance configuration. A missing key is a
// configuration state reported by Search, not a construction failure.
func New(cfg Config) (*Client, error) {
	base, err := web.ResolveServiceBaseURL(cfg.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, err
	}
	if err := ParseOptions(cfg.Options); err != nil {
		return nil, web.NewError(web.CodeInvalidConfig, "%v", err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		instanceID: cfg.InstanceID,
		base:       base,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		timeout:    timeout,
		http:       web.NewServiceHTTPClient(timeout, cfg.Transport),
		now:        now,
	}, nil
}

// Origin returns the endpoint origin for permission fingerprints and display.
func (c *Client) Origin() string { return web.ServiceOrigin(c.base) }

// Capabilities reports the constraints the query operators enforce:
// site: for an allow list and -site: for exclusions.
func (c *Client) Capabilities() web.SearchCapabilities {
	return web.SearchCapabilities{AllowedDomains: true, ExcludedDomains: true}
}

// Close releases idle connections.
func (c *Client) Close() { web.CloseIdle(c.http) }

// Search performs exactly one POST /v1/tools/search. Paid requests are never
// retried.
func (c *Client) Search(ctx context.Context, request web.SearchRequest) (web.SearchResponse, error) {
	if ctx == nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidArgument, "context is required")
	}
	if err := request.Validate(); err != nil {
		return web.SearchResponse{}, err
	}
	if c.apiKey == "" {
		return web.SearchResponse{}, web.NewError(web.CodeMissingCredentials, "instance %q has no API key", c.instanceID)
	}
	query, err := Query(request)
	if err != nil {
		return web.SearchResponse{}, err
	}
	count := request.MaxResults
	if count == 0 {
		count = web.DefaultMaxResults
	}
	body, err := json.Marshal(searchRequest{Query: query, Count: count})
	if err != nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidArgument, "encode request: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	endpoint := *c.base
	endpoint.Path = c.base.Path + "/v1/tools/search"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidConfig, "build request: %v", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "aice/"+buildinfo.Version)
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)

	started := c.now()
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return web.SearchResponse{}, web.WrapContextError(err, web.CodeUnavailable, "search service request failed")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidResponse, "search endpoint answered with redirect %d; redirects are not followed", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		message := ""
		if payload, err := web.ReadBounded(response.Body, maxErrorBodyBytes); err == nil {
			var envelope errorResponse
			if json.Unmarshal(payload, &envelope) == nil {
				message = envelope.Error.Message
			}
		}
		return web.SearchResponse{}, web.StatusError(response, message)
	}
	payload, err := web.ReadBounded(response.Body, maxSuccessBodyBytes)
	if err != nil {
		return web.SearchResponse{}, err
	}
	var decoded searchResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidResponse, "search response is not valid JSON")
	}
	if decoded.Results == nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidResponse, "search response has no results field")
	}
	rows := make([]web.ResultRow, len(*decoded.Results))
	for index, result := range *decoded.Results {
		// summary is the passage DeepSeek extracts for a model to read; the
		// short display snippet stands in only when it is missing.
		excerpt := result.Summary
		if strings.TrimSpace(excerpt) == "" {
			excerpt = result.Snippet
		}
		rows[index] = web.ResultRow{URL: result.URL, Title: result.Title, Published: result.Date, Excerpts: []string{excerpt}}
	}
	bundle, err := web.NormalizeResults(rows, c.now())
	if err != nil {
		return web.SearchResponse{}, err
	}
	bundle.Diagnostics.DurationMS = c.now().Sub(started).Milliseconds()
	bundle.Diagnostics.BytesReceived = int64(len(payload))
	return web.SearchResponse{
		InstanceID: c.instanceID,
		ProviderID: ProviderID,
		APIID:      APIID,
		Query:      strings.TrimSpace(request.Query),
		Evidence:   bundle,
	}, nil
}

// Query expresses the request's domain constraints as DeepSeek query
// operators: allowed domains join as site: alternatives and exclusions add
// -site: terms. The result must fit the API's character limit, which the
// operators count toward.
func Query(request web.SearchRequest) (string, error) {
	terms := []string{strings.TrimSpace(request.Query)}
	if len(request.AllowedDomains) > 0 {
		sites := make([]string, len(request.AllowedDomains))
		for index, domain := range request.AllowedDomains {
			sites[index] = "site:" + domain
		}
		terms = append(terms, strings.Join(sites, " OR "))
	}
	for _, domain := range request.ExcludedDomains {
		terms = append(terms, "-site:"+domain)
	}
	query := strings.Join(terms, " ")
	if length := utf8.RuneCountInString(query); length > MaxQueryRunes {
		return "", web.NewError(web.CodeInvalidArgument, "query is %d characters with domain operators; DeepSeek search accepts at most %d, so shorten the query or use fewer domains", length, MaxQueryRunes)
	}
	return query, nil
}

type searchRequest struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// searchResponse tolerates unknown fields; the presence of "results" is
// checked separately from an empty array.
type searchResponse struct {
	Results *[]searchResult `json:"results"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Summary string `json:"summary"`
	Date    string `json:"date"`
}

// errorResponse is the documented OpenAI-style error envelope.
type errorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}
