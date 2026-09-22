package httpfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"github.com/ch1lam/aice-cli/internal/buildinfo"
	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/web"
)

// Product defaults. Bodies are bounded before and after decompression.
const (
	DefaultTimeout      = 30 * time.Second
	DefaultMaxBodyBytes = 5 * 1024 * 1024
	DefaultMaxRedirects = 5
	// MaxDocumentBytes bounds the extracted text retained as evidence.
	MaxDocumentBytes = 32 * 1024
	sniffBytes       = 512
)

// Config constructs one Fetcher. The HTTP client is injectable so tests use a
// fake transport and never touch real DNS or the internet.
type Config struct {
	Timeout      time.Duration
	MaxBodyBytes int64
	MaxRedirects int
	Now          func() time.Time
	// RootCAs replaces the system roots for tests with local certificates.
	// Verification itself is never disabled and always uses the URL hostname.
	RootCAs *x509.CertPool
	// Client overrides the default HTTP client. Tests inject a fake
	// transport; production uses a clone of http.DefaultTransport. The
	// redirect policy is always enforced.
	Client *http.Client
}

// Fetcher retrieves public pages. It is safe for concurrent use.
type Fetcher struct {
	cfg    Config
	client *http.Client
}

// New applies defaults and validates the configuration.
func New(cfg Config) (*Fetcher, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if cfg.MaxRedirects < 0 {
		return nil, fmt.Errorf("httpfetch: max redirects cannot be negative")
	}
	if cfg.MaxRedirects == 0 {
		cfg.MaxRedirects = DefaultMaxRedirects
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Transport: defaultTransport(cfg.Timeout, cfg.RootCAs)}
	} else {
		cloned := *client
		if cloned.Transport == nil {
			cloned.Transport = defaultTransport(cfg.Timeout, cfg.RootCAs)
		}
		client = &cloned
	}
	// Redirects are handled by Fetch so every hop is revalidated under the
	// same permission and address policy.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.Client = client
	return &Fetcher{cfg: cfg, client: client}, nil
}

// defaultTransport clones http.DefaultTransport to keep the standard proxy,
// dial and connection behavior (including ProxyFromEnvironment), then applies
// the fetch-specific bounds. The global default is never mutated.
func defaultTransport(timeout time.Duration, roots *x509.CertPool) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{
			Proxy:       http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		}
	}
	transport.DisableKeepAlives = true
	transport.DisableCompression = true // decompress manually to bound decoded bytes
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = timeout
	transport.MaxResponseHeaderBytes = 64 * 1024
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return transport
}

// Fetch performs one bounded GET, following at most MaxRedirects redirects
// with each hop revalidated, then extracts readable text.
func (f *Fetcher) Fetch(ctx context.Context, request web.FetchRequest) (web.FetchResponse, error) {
	if ctx == nil {
		return web.FetchResponse{}, web.NewError(web.CodeInvalidArgument, "context is required")
	}
	if err := request.Validate(); err != nil {
		return web.FetchResponse{}, err
	}
	format := request.Format
	if format == "" {
		format = web.FetchFormatMarkdown
	}
	first, err := validateURL(request.URL)
	if err != nil {
		return web.FetchResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	current := first
	var response *http.Response
	for hop := 0; ; hop++ {
		response, err = f.get(ctx, current)
		if err != nil {
			return web.FetchResponse{}, err
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			break
		}
		location := response.Header.Get("Location")
		drain(response)
		if location == "" {
			return web.FetchResponse{}, web.NewError(web.CodeInvalidResponse, "redirect %d without Location", response.StatusCode)
		}
		if hop+1 > f.cfg.MaxRedirects {
			return web.FetchResponse{}, web.NewError(web.CodeRedirectRefused, "more than %d redirects", f.cfg.MaxRedirects)
		}
		next, err := f.nextHop(current, location)
		if err != nil {
			return web.FetchResponse{}, err
		}
		current = next
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response)
		return web.FetchResponse{}, classifyStatus(response.StatusCode)
	}
	body, mediaType, warnings, err := f.readBody(response)
	if err != nil {
		return web.FetchResponse{}, err
	}
	extracted, err := extract(ctx, body, mediaType, format, current.url)
	if err != nil {
		return web.FetchResponse{}, err
	}
	warnings = append(warnings, extracted.warnings...)

	source, err := evidence.NewSource(current.url.String(), extracted.title, "")
	if err != nil {
		return web.FetchResponse{}, web.NewError(web.CodeInvalidResponse, "final url: %v", err)
	}
	text, truncated := web.TruncateUTF8(extracted.text, MaxDocumentBytes)
	itemFormat := evidence.FormatText
	if format == web.FetchFormatMarkdown {
		itemFormat = evidence.FormatMarkdown
	}
	bundle := evidence.Bundle{
		Sources: []evidence.Source{source},
		Items: []evidence.Evidence{{
			SourceID: source.ID, Kind: evidence.KindDocument, Text: text, Format: itemFormat,
			Acquisition: evidence.AcquisitionHTTPFetch, RetrievedAt: f.cfg.Now().UnixMilli(),
			Truncated: truncated, ReturnedBytes: len(text),
		}},
		Diagnostics: evidence.Diagnostics{Warnings: warnings, BytesReceived: int64(len(body))},
	}
	if err := bundle.Validate(); err != nil {
		return web.FetchResponse{}, web.NewError(web.CodeInvalidResponse, "fetched content is invalid: %v", err)
	}
	return web.FetchResponse{
		RequestedURL: first.url.String(),
		FinalURL:     current.url.String(),
		HTTPStatus:   response.StatusCode,
		MediaType:    mediaType,
		Extraction:   extracted.method,
		Evidence:     bundle,
	}, nil
}

// get performs one hop: lightweight literal check, then request. DNS,
// proxy selection and dialing are left to the standard HTTP transport.
func (f *Fetcher) get(ctx context.Context, hop target) (*http.Response, error) {
	if err := checkFetchTarget(hop); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, hop.url.String(), nil)
	if err != nil {
		return nil, web.NewError(web.CodeInvalidArgument, "build request: %v", err)
	}
	request.Header.Set("User-Agent", "aice/"+buildinfo.Version)
	request.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, text/markdown;q=0.9, */*;q=0.1")
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := f.client.Do(request)
	if err != nil {
		var classified *web.Error
		if errors.As(err, &classified) {
			return nil, classified
		}
		return nil, web.WrapContextError(err, web.CodeUnavailable, fmt.Sprintf("request to %s failed", hop.origin))
	}
	return response, nil
}

// nextHop validates a redirect target under the same policy as the first
// URL, with no https→http downgrade. Cross-origin redirects continue
// automatically once each hop passes URL and literal-target checks.
func (f *Fetcher) nextHop(current target, location string) (target, error) {
	resolved, err := current.url.Parse(location)
	if err != nil {
		return target{}, web.NewError(web.CodeInvalidResponse, "redirect location %q is invalid", web.Sanitize(location))
	}
	next, err := validateURL(resolved.String())
	if err != nil {
		return target{}, &web.Error{Code: web.CodeRedirectRefused, Message: "redirect target refused: " + err.Error()}
	}
	if current.url.Scheme == "https" && next.url.Scheme != "https" {
		return target{}, web.NewError(web.CodeRedirectRefused, "redirect from https to http refused: %s", next.url)
	}
	return next, nil
}

func classifyStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusProxyAuthRequired:
		return web.NewError(web.CodeAuthentication, "HTTP %d: the page requires authentication, which web_fetch does not support", status)
	case status == http.StatusTooManyRequests:
		return web.NewError(web.CodeRateLimited, "HTTP %d: the site is rate limiting requests", status)
	case status == http.StatusNotFound || status == http.StatusGone:
		return web.NewError(web.CodeUnavailable, "HTTP %d: the page was not found", status)
	case status >= 500:
		return web.NewError(web.CodeUnavailable, "HTTP %d: the server failed", status)
	default:
		return web.NewError(web.CodeUnavailable, "HTTP %d", status)
	}
}

// readBody enforces the raw and decoded byte limits, decodes the charset to
// UTF-8 and returns the sniffed or declared media type.
func (f *Fetcher) readBody(response *http.Response) ([]byte, string, []string, error) {
	var warnings []string
	raw, err := readLimited(response.Body, f.cfg.MaxBodyBytes)
	if err != nil {
		return nil, "", nil, err
	}
	if strings.EqualFold(strings.TrimSpace(response.Header.Get("Content-Encoding")), "gzip") {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, "", nil, web.NewError(web.CodeInvalidResponse, "gzip body is invalid")
		}
		raw, err = readLimited(reader, f.cfg.MaxBodyBytes)
		if err != nil {
			return nil, "", nil, err
		}
	} else if encoding := response.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, "", nil, web.NewError(web.CodeUnsupportedContent, "content encoding %q is not supported", web.Sanitize(encoding))
	}

	declared := response.Header.Get("Content-Type")
	mediaType, params, parseErr := mime.ParseMediaType(declared)
	if parseErr != nil || declared == "" {
		mediaType = ""
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "" || mediaType == "application/octet-stream" {
		sniffed := strings.ToLower(http.DetectContentType(raw[:min(len(raw), sniffBytes)]))
		switch {
		case strings.HasPrefix(sniffed, "text/html"):
			mediaType = "text/html"
		case strings.HasPrefix(sniffed, "text/plain"):
			mediaType = "text/plain"
		default:
			return nil, "", nil, web.NewError(web.CodeUnsupportedContent, "content type %q is not supported", web.Sanitize(sniffed))
		}
		warnings = append(warnings, "content type was missing or generic; used sniffed "+mediaType)
	}
	switch mediaType {
	case "text/plain", "text/markdown", "text/html", "application/xhtml+xml":
	default:
		return nil, "", nil, web.NewError(web.CodeUnsupportedContent, "content type %q is not supported; web_fetch handles text, Markdown and HTML", web.Sanitize(mediaType))
	}

	// charset.NewReader honours the declared charset, a BOM or an HTML meta
	// declaration and converts to UTF-8. It never guesses binary as text.
	contentType := mediaType
	if cs, ok := params["charset"]; ok {
		contentType += "; charset=" + cs
	}
	decoded, err := charset.NewReader(bytes.NewReader(raw), contentType)
	if err != nil {
		return nil, "", nil, web.NewError(web.CodeUnsupportedContent, "charset conversion failed: %v", err)
	}
	body, err := readLimited(decoded, f.cfg.MaxBodyBytes)
	if err != nil {
		return nil, "", nil, err
	}
	if !utf8.Valid(body) {
		warnings = append(warnings, "body contained invalid UTF-8 after decoding; invalid sequences were replaced")
		body = bytes.ToValidUTF8(body, []byte("\uFFFD"))
	}
	return body, mediaType, warnings, nil
}

// readLimited reads at most limit bytes and reports when the source has more.
func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, web.WrapContextError(err, web.CodeUnavailable, "reading the response body failed")
	}
	if int64(len(data)) > limit {
		return nil, web.NewError(web.CodeResponseTooLarge, "response body exceeds %d bytes", limit)
	}
	return data, nil
}

func drain(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	_ = response.Body.Close()
}

// resolveLink resolves a possibly relative reference against the validated
// final page URL, ignoring any untrusted <base> element.
func resolveLink(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	resolved, err := base.Parse(href)
	if err != nil {
		return ""
	}
	switch strings.ToLower(resolved.Scheme) {
	case "http", "https", "mailto":
		return resolved.String()
	default:
		return ""
	}
}
