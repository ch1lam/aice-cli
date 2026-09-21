package exa

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
	base, err := ResolveBaseURL(cfg.BaseURL)
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
	transport := cfg.Transport
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
		http: &http.Client{
			Transport: transport,
			// API requests never follow redirects: a moved endpoint must not
			// receive the x-api-key header on another origin.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: now,
	}, nil
}

// Origin returns the endpoint origin for permission fingerprints and display.
func (c *Client) Origin() string { return Origin(c.base) }

// Capabilities reports the constraints Exa enforces server-side.
func (c *Client) Capabilities() web.SearchCapabilities {
	return web.SearchCapabilities{AllowedDomains: true, ExcludedDomains: true, MaxResults: maxUpstreamResults}
}

// Close releases idle connections.
func (c *Client) Close() {
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

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
	payload, err := readBounded(response.Body, maxSuccessBodyBytes)
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
	payload, readErr := readBounded(response.Body, maxErrorBodyBytes)
	message := ""
	if readErr == nil {
		var envelope errorResponse
		if json.Unmarshal(payload, &envelope) == nil {
			message = errorMessage(envelope.Error)
			if envelope.Tag != "" {
				message = strings.TrimSpace(envelope.Tag + " " + message)
			}
		}
	}
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	message = web.Sanitize(message)
	code := web.CodeUnavailable
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		code = web.CodeAuthentication
	case response.StatusCode == http.StatusPaymentRequired:
		code = web.CodeQuotaExceeded
	case response.StatusCode == http.StatusTooManyRequests:
		code = web.CodeRateLimited
	case response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnprocessableEntity:
		code = web.CodeInvalidArgument
	case response.StatusCode >= 500:
		code = web.CodeUnavailable
	default:
		code = web.CodeInvalidResponse
	}
	classified := &web.Error{Code: code, Message: fmt.Sprintf("search service returned HTTP %d: %s", response.StatusCode, message)}
	if retry := response.Header.Get("Retry-After"); retry != "" {
		classified.RetryAfter = retry
	}
	return classified
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

// readBounded reads at most limit bytes and fails when more are available,
// independently of Content-Length and chunked encoding.
func readBounded(body io.Reader, limit int) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, web.WrapContextError(err, web.CodeUnavailable, "read search response")
		}
		return nil, web.WrapContextError(err, web.CodeUnavailable, "read search response failed")
	}
	if len(payload) > limit {
		return nil, web.NewError(web.CodeResponseTooLarge, "search response exceeds %d bytes", limit)
	}
	return payload, nil
}
