package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Client is one configured Exa instance. It is safe for concurrent use and
// owns its HTTP transport; the credentialed client is never shared with the
// page fetcher.
type Client struct {
	instanceID string
	base       *url.URL
	apiKey     string
	options    Options
	timeout    time.Duration
	http       *http.Client
	now        func() time.Time
}

// New validates the instance configuration and constructs a client. A missing
// key is a configuration state, not a construction failure; Search reports it.
func New(cfg Config) (*Client, error) {
	base, err := web.ResolveServiceBaseURL(cfg.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, err
	}
	options, err := ParseOptions(cfg.Options)
	if err != nil {
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
		options:    options,
		timeout:    timeout,
		http:       web.NewServiceHTTPClient(timeout, cfg.Transport),
		now:        now,
	}, nil
}

// Origin returns the endpoint origin for permission fingerprints and display.
func (c *Client) Origin() string { return web.ServiceOrigin(c.base) }

// Capabilities reports the constraints Exa enforces server-side.
func (c *Client) Capabilities() web.SearchCapabilities {
	return web.SearchCapabilities{AllowedDomains: true, ExcludedDomains: true}
}

// Close releases idle connections.
func (c *Client) Close() { web.CloseIdle(c.http) }

// Search performs exactly one POST /search. It never retries a paid request.
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
	numResults := request.MaxResults
	if numResults == 0 {
		numResults = web.DefaultMaxResults
	}
	body, err := json.Marshal(searchRequest{
		Query:          strings.TrimSpace(request.Query),
		Type:           c.options.Type,
		NumResults:     numResults,
		IncludeDomains: request.AllowedDomains,
		ExcludeDomains: request.ExcludedDomains,
		Contents:       requestContents{Text: false, Highlights: true},
	})
	if err != nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidArgument, "encode request: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	endpoint := *c.base
	endpoint.Path = c.base.Path + "/search"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidConfig, "build request: %v", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "aice/"+buildinfo.Version)
	httpRequest.Header.Set("x-api-key", c.apiKey)

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
		return web.SearchResponse{}, c.classifyError(response)
	}
	payload, err := web.ReadBounded(response.Body, maxSuccessBodyBytes)
	if err != nil {
		return web.SearchResponse{}, err
	}
	var decoded searchResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidResponse, "search response is not valid JSON")
	}
	if len(decoded.Error) > 0 && string(decoded.Error) != "null" {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidResponse, "search response carried an error envelope with status 200: %s", errorMessage(decoded.Error))
	}
	if decoded.Results == nil {
		return web.SearchResponse{}, web.NewError(web.CodeInvalidResponse, "search response has no results field")
	}
	bundle, err := normalizeResults(*decoded.Results, c.now())
	if err != nil {
		return web.SearchResponse{}, err
	}
	bundle.Diagnostics.UpstreamRequestID = decoded.RequestID
	bundle.Diagnostics.DurationMS = c.now().Sub(started).Milliseconds()
	bundle.Diagnostics.BytesReceived = int64(len(payload))
	if decoded.CostDollars != nil && decoded.CostDollars.Total != nil {
		bundle.Diagnostics.ReportedCost = costOf(*decoded.CostDollars.Total)
	}
	return web.SearchResponse{
		InstanceID: c.instanceID,
		ProviderID: ProviderID,
		APIID:      APIID,
		Query:      strings.TrimSpace(request.Query),
		Evidence:   bundle,
	}, nil
}

func (c *Client) classifyError(response *http.Response) error {
	message := ""
	if payload, err := web.ReadBounded(response.Body, maxErrorBodyBytes); err == nil {
		var envelope errorResponse
		if json.Unmarshal(payload, &envelope) == nil {
			message = errorMessage(envelope.Error)
			if envelope.Tag != "" {
				message = strings.TrimSpace(envelope.Tag + " " + message)
			}
		}
	}
	return web.StatusError(response, message)
}

func errorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return object.Message
	}
	return ""
}
