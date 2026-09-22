package httpfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/web"
	"golang.org/x/net/http/httpproxy"
)

// roundTripFunc is a fake HTTP transport. Tests answer public-looking
// hostnames in memory without real DNS or network; literal-target checks
// still run before the transport is used.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type harness struct {
	calls atomic.Int32
	stub  func(request *http.Request) (*http.Response, error)
}

func (h *harness) transport() http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		h.calls.Add(1)
		return h.stub(request)
	})
}

func (h *harness) fetcher(t *testing.T, mutate func(*Config)) *Fetcher {
	t.Helper()
	cfg := Config{
		Client:  &http.Client{Transport: h.transport()},
		Timeout: 5 * time.Second,
		Now:     func() time.Time { return time.UnixMilli(1700000000000) },
	}
	if mutate != nil {
		mutate(&cfg)
		// The mutation may replace the client; make sure the fake transport
		// and the redirect policy still apply.
		if cfg.Client == nil {
			cfg.Client = &http.Client{Transport: h.transport()}
		} else if _, ok := cfg.Client.Transport.(roundTripFunc); !ok && cfg.Client.Transport != h.transport() {
			// A test-provided client without our stub keeps its own
			// transport; request counting below does not apply to it.
		}
	}
	fetcher, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return fetcher
}

func textResponse(request *http.Request, contentType, body string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func redirectResponse(request *http.Request, location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{location}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// slowBody blocks until the request context ends, simulating a stalled body.
type slowBody struct {
	ctx context.Context
}

func (b slowBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b slowBody) Close() error { return nil }

// endlessReader yields an unbounded stream so the byte limit stops the fetch.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'c'
	}
	return len(p), nil
}

const samplePage = `<!DOCTYPE html><html><head><meta charset="utf-8"><title> Sample  Page </title>
<script>alert("x")</script><style>body{}</style><base href="https://evil.test/"></head>
<body><nav><a href="/nav">Nav</a></nav><header>Site header</header>
<main><h1>Heading <em>one</em></h1><p>First   paragraph with <a href="/docs/a">a link</a> and <strong>bold</strong> text.</p>
<pre><code>func main() {
	fmt.Println("indent kept")
}</code></pre>
<ul><li>Item one</li><li>Item <code>two</code><ul><li>Nested</li></ul></li></ul>
<ol start="3"><li>Third</li></ol>
<table><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody><tr><td>a</td><td>1 | 2</td></tr></tbody></table>
<blockquote><p>Quoted</p></blockquote>
<img src="https://example.test/x.png" alt="An image"><iframe src="https://evil.test"></iframe>
<p hidden>hidden text</p><div aria-hidden="true">shown despite aria</div></main>
<footer>Footer noise</footer><noscript>noscript</noscript></body></html>`

func TestFetchHTMLExtractsMarkdown(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var gotHost, gotAgent, gotEncoding string
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		gotHost, gotAgent, gotEncoding = request.URL.Host, request.Header.Get("User-Agent"), request.Header.Get("Accept-Encoding")
		return textResponse(request, "text/html; charset=utf-8", samplePage), nil
	}}
	response, err := h.fetcher(t, nil).Fetch(t.Context(), web.FetchRequest{URL: "HTTP://Example.test/docs/page?x=1"})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || gotHost != "example.test" || !strings.HasPrefix(gotAgent, "aice/") || gotEncoding != "gzip" {
		t.Fatalf("request = %d %q %q %q", requests.Load(), gotHost, gotAgent, gotEncoding)
	}
	if response.RequestedURL != "http://example.test/docs/page?x=1" || response.FinalURL != response.RequestedURL || response.HTTPStatus != 200 || response.MediaType != "text/html" || response.Extraction != "main" {
		t.Fatalf("response = %+v", response)
	}
	text := response.Evidence.Items[0].Text
	for _, want := range []string{
		"# Heading *one*",
		"First paragraph with [a link](http://example.test/docs/a) and **bold** text.",
		"```\nfunc main() {\n\tfmt.Println(\"indent kept\")\n}\n```",
		"- Item one\n- Item `two`\n  - Nested",
		"3. Third",
		"| Name | Value |\n| --- | --- |\n| a | 1 \\| 2 |",
		"> Quoted",
		"[image: An image]",
		"shown despite aria",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"alert", "body{}", "Nav", "Site header", "Footer noise", "noscript", "hidden text", "evil.test"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("markdown contains %q:\n%s", forbidden, text)
		}
	}
	source := response.Evidence.Sources[0]
	if source.Title != "Sample Page" || source.URL != response.FinalURL || source.PublishedAt != "" {
		t.Fatalf("source = %+v", source)
	}
	item := response.Evidence.Items[0]
	if item.Kind != evidence.KindDocument || item.Format != evidence.FormatMarkdown || item.Acquisition != evidence.AcquisitionHTTPFetch || item.RetrievedAt != 1700000000000 || item.Truncated {
		t.Fatalf("item = %+v", item)
	}

	plain, err := h.fetcher(t, nil).Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/", Format: web.FetchFormatText})
	if err != nil {
		t.Fatal(err)
	}
	if text := plain.Evidence.Items[0].Text; strings.Contains(text, "](") || strings.Contains(text, "**") || !strings.Contains(text, "a link") || plain.Evidence.Items[0].Format != evidence.FormatText {
		t.Fatalf("text format = %q", text)
	}
}

func TestFetchPlainMarkdownAndFallbacks(t *testing.T) {
	t.Parallel()
	pages := map[string]struct {
		contentType, body string
	}{
		"/plain":    {"text/plain; charset=utf-8", "line one\r\nline two"},
		"/md":       {"text/markdown", "# Title\n\nbody"},
		"/article":  {"text/html", "<html><body><header>chrome</header><article><p>Article body</p></article></body></html>"},
		"/body":     {"text/html", "<html><body><header>Kept header</header><p>Body fallback</p></body></html>"},
		"/sniff":    {"", "<html><body><p>Sniffed html</p></body></html>"},
		"/binary":   {"application/octet-stream", "\x00\x01\x02\x03binary"},
		"/pdf":      {"application/pdf", "%PDF-1.4"},
		"/latin1":   {"text/html; charset=iso-8859-1", "<html><body><p>caf\xe9</p></body></html>"},
		"/metacs":   {"text/html", "<html><head><meta charset=\"iso-8859-1\"></head><body><p>na\xefve</p></body></html>"},
		"/xhtml":    {"application/xhtml+xml", "<html xmlns=\"http://www.w3.org/1999/xhtml\"><body><p>xhtml</p></body></html>"},
		"/empty":    {"text/html", "<html><body><script>only()</script></body></html>"},
		"/brotli":   {"text/html", "x"},
		"/notfound": {"text/html", "missing"},
		"/auth":     {"text/html", "login"},
		"/rate":     {"text/html", "slow"},
		"/server":   {"text/html", "boom"},
	}
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		page := pages[request.URL.Path]
		response := textResponse(request, page.contentType, page.body)
		switch request.URL.Path {
		case "/brotli":
			response.Header.Set("Content-Encoding", "br")
		case "/notfound":
			response.StatusCode = 404
		case "/auth":
			response.StatusCode = 401
		case "/rate":
			response.StatusCode = 429
		case "/server":
			response.StatusCode = 503
		}
		return response, nil
	}}
	fetcher := h.fetcher(t, nil)
	cases := []struct {
		path, wantText, wantMethod, wantMedia string
		wantCode                              web.ErrorCode
		wantWarning                           bool
	}{
		{path: "/plain", wantText: "line one\nline two", wantMethod: "text", wantMedia: "text/plain"},
		{path: "/md", wantText: "# Title\n\nbody", wantMethod: "markdown", wantMedia: "text/markdown"},
		{path: "/article", wantText: "Article body", wantMethod: "article"},
		{path: "/body", wantText: "Body fallback", wantMethod: "body"},
		{path: "/sniff", wantText: "Sniffed html", wantMethod: "body", wantWarning: true},
		{path: "/binary", wantCode: web.CodeUnsupportedContent},
		{path: "/pdf", wantCode: web.CodeUnsupportedContent},
		{path: "/latin1", wantText: "café"},
		{path: "/metacs", wantText: "naïve"},
		{path: "/xhtml", wantText: "xhtml", wantMedia: "application/xhtml+xml"},
		{path: "/empty", wantText: "", wantWarning: true},
		{path: "/brotli", wantCode: web.CodeUnsupportedContent},
		{path: "/notfound", wantCode: web.CodeUnavailable},
		{path: "/auth", wantCode: web.CodeAuthentication},
		{path: "/rate", wantCode: web.CodeRateLimited},
		{path: "/server", wantCode: web.CodeUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			response, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test" + tc.path})
			if tc.wantCode != "" {
				if web.CodeOf(err) != tc.wantCode {
					t.Fatalf("err = %v, want %s", err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := response.Evidence.Items[0].Text
			exact := tc.wantMethod == "text" || tc.wantMethod == "markdown" || tc.wantText == ""
			if (exact && got != tc.wantText) || (!exact && !strings.Contains(got, tc.wantText)) {
				t.Fatalf("text = %q, want %q", got, tc.wantText)
			}
			if tc.path == "/body" && strings.Contains(got, "Kept header") {
				t.Fatal("body fallback kept site header chrome")
			}
			if tc.wantMethod != "" && response.Extraction != tc.wantMethod {
				t.Fatalf("method = %q", response.Extraction)
			}
			if tc.wantMedia != "" && response.MediaType != tc.wantMedia {
				t.Fatalf("media = %q", response.MediaType)
			}
			if tc.wantWarning != (len(response.Evidence.Diagnostics.Warnings) > 0) {
				t.Fatalf("warnings = %v", response.Evidence.Diagnostics.Warnings)
			}
		})
	}
}

func TestFetchRefusesBlockedTargetsWithoutRequest(t *testing.T) {
	t.Parallel()
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		return textResponse(request, "text/plain", "should not be reached"), nil
	}}
	fetcher := h.fetcher(t, nil)
	cases := []struct {
		url      string
		wantCode web.ErrorCode
	}{
		{"http://127.0.0.1/", web.CodeBlockedTarget},
		{"http://localhost/", web.CodeBlockedTarget},
		{"http://LOCALHOST/", web.CodeBlockedTarget},
		{"http://10.0.0.1/", web.CodeBlockedTarget},
		{"http://[::1]/", web.CodeBlockedTarget},
		{"http://[::ffff:127.0.0.1]/", web.CodeBlockedTarget},
		{"http://[::ffff:10.0.0.1]/", web.CodeBlockedTarget},
		{"http://[fe80::1%25eth0]/", web.CodeBlockedTarget},
		{"http://169.254.169.254/latest/meta-data", web.CodeBlockedTarget},
		{"http://100.64.0.1/", web.CodeBlockedTarget},
		{"http://0.0.0.0/", web.CodeBlockedTarget},
		{"http://255.255.255.255/", web.CodeBlockedTarget},
		{"http://192.0.2.1/", web.CodeBlockedTarget},
		{"http://[fd00::1]/", web.CodeBlockedTarget},
		{"http://[64:ff9b::a00:1]/", web.CodeBlockedTarget},
		{"http://example.test:8080/", web.CodeBlockedTarget},
		{"https://example.test:80/", web.CodeBlockedTarget},
		{"ftp://example.test/", web.CodeInvalidArgument},
		{"file:///etc/passwd", web.CodeInvalidArgument},
		{"javascript:alert(1)", web.CodeInvalidArgument},
		{"gopher://example.test/", web.CodeInvalidArgument},
		{"http://user:pw@example.test/", web.CodeInvalidArgument},
		{"http://user@example.test/", web.CodeInvalidArgument},
		{"example.test/relative", web.CodeInvalidArgument},
		{"http:///nohost", web.CodeInvalidArgument},
		{"http://exa mple.test/", web.CodeInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			_, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: tc.url})
			if web.CodeOf(err) != tc.wantCode {
				t.Fatalf("err = %v, want %s", err, tc.wantCode)
			}
		})
	}
	if h.calls.Load() != 0 {
		t.Fatalf("blocked targets were requested %d times", h.calls.Load())
	}
}

func TestFetchRedirectPolicy(t *testing.T) {
	t.Parallel()
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "other.test" && request.URL.Path == "/final" {
			return textResponse(request, "text/plain", "arrived at /final"), nil
		}
		switch request.URL.Path {
		case "/same":
			return redirectResponse(request, "/final"), nil
		case "/relative":
			response := redirectResponse(request, "sub/final")
			response.StatusCode = http.StatusMovedPermanently
			return response, nil
		case "/sub/final", "/final":
			return textResponse(request, "text/plain", "arrived at "+request.URL.Path), nil
		case "/cross":
			return redirectResponse(request, "http://other.test/final"), nil
		case "/literal":
			return redirectResponse(request, "http://127.0.0.1/final"), nil
		case "/private-literal":
			return redirectResponse(request, "http://10.0.0.1/final"), nil
		case "/port":
			return redirectResponse(request, "http://example.test:8080/final"), nil
		case "/loop":
			return redirectResponse(request, "/loop"), nil
		case "/nolocation":
			return &http.Response{StatusCode: http.StatusFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		case "/scheme":
			return redirectResponse(request, "file:///etc/passwd"), nil
		default:
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing")), Request: request}, nil
		}
	}}
	fetcher := h.fetcher(t, nil)

	response, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/same"})
	if err != nil || response.FinalURL != "http://example.test/final" || response.RequestedURL != "http://example.test/same" || !strings.Contains(response.Evidence.Items[0].Text, "arrived at /final") {
		t.Fatalf("same-origin redirect: %+v %v", response, err)
	}
	if response.Evidence.Sources[0].URL != "http://example.test/final" {
		t.Fatal("source must be the final url")
	}
	response, err = fetcher.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/relative"})
	if err != nil || response.FinalURL != "http://example.test/sub/final" {
		t.Fatalf("relative redirect: %+v %v", response, err)
	}
	// Legal cross-origin redirects continue automatically once the target
	// passes the URL and literal-target checks.
	response, err = fetcher.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/cross"})
	if err != nil || response.FinalURL != "http://other.test/final" || !strings.Contains(response.Evidence.Items[0].Text, "arrived at /final") {
		t.Fatalf("cross-origin redirect: %+v %v", response, err)
	}
	if response.Evidence.Sources[0].URL != "http://other.test/final" {
		t.Fatal("source must be the final url")
	}
	for path, want := range map[string]web.ErrorCode{
		"/literal": web.CodeBlockedTarget, "/private-literal": web.CodeBlockedTarget,
		"/port": web.CodeRedirectRefused, "/loop": web.CodeRedirectRefused, "/nolocation": web.CodeInvalidResponse, "/scheme": web.CodeRedirectRefused,
	} {
		_, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test" + path})
		if web.CodeOf(err) != want {
			t.Fatalf("%s: err = %v, want %s", path, err, want)
		}
	}
}

func TestFetchHTTPSDowngradeRefused(t *testing.T) {
	t.Parallel()
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/downgrade" {
			return redirectResponse(request, "http://example.test/final"), nil
		}
		return textResponse(request, "text/plain", "secure "+request.URL.Host), nil
	}}
	fetcher := h.fetcher(t, nil)
	response, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "https://example.test/page"})
	if err != nil || response.Evidence.Items[0].Text != "secure example.test" {
		t.Fatalf("https fetch: %+v %v", response, err)
	}
	if _, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "https://example.test/downgrade"}); web.CodeOf(err) != web.CodeRedirectRefused || !strings.Contains(err.Error(), "https to http") {
		t.Fatalf("downgrade accepted: %v", err)
	}
}

func TestFetchDefaultTransportFollowsProxyEnvironment(t *testing.T) {
	// net/http caches ProxyFromEnvironment after its first use in the
	// process, so env-sensitive assertions go through
	// golang.org/x/net/http/httpproxy, which implements the same standard
	// selection without the cache. The transport itself must wire the
	// standard function instead of custom logic.
	transport := defaultTransport(DefaultTimeout, nil)
	if transport.Proxy == nil {
		t.Fatal("default transport has no proxy function")
	}
	if reflect.ValueOf(transport.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("default transport must use http.ProxyFromEnvironment, not custom proxy logic")
	}
	proxyURL, err := url.Parse("http://proxy.test:8080")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_PROXY", proxyURL.String())
	t.Setenv("http_proxy", proxyURL.String())
	t.Setenv("HTTPS_PROXY", proxyURL.String())
	t.Setenv("https_proxy", proxyURL.String())
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	got, err := httpproxy.FromEnvironment().ProxyFunc()(request.URL)
	if err != nil || got == nil || got.String() != proxyURL.String() {
		t.Fatalf("proxy = %v, %v, want %s", got, err, proxyURL)
	}
	t.Setenv("NO_PROXY", "example.test")
	t.Setenv("no_proxy", "example.test")
	got, err = httpproxy.FromEnvironment().ProxyFunc()(request.URL)
	if err != nil || got != nil {
		t.Fatalf("NO_PROXY bypass = %v, %v, want nil", got, err)
	}
}

func TestFetchDefaultClientLeavesGlobalTransportAlone(t *testing.T) {
	t.Parallel()
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not a *http.Transport")
	}
	beforeCompression, beforeHeaders := base.DisableCompression, base.MaxResponseHeaderBytes
	fetcher, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.client.Transport == base {
		t.Fatal("fetcher must not reuse the global transport instance")
	}
	transport, ok := fetcher.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", fetcher.client.Transport)
	}
	if !transport.DisableCompression {
		t.Fatal("default client must decompress manually to keep body limits")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("tls config = %+v", transport.TLSClientConfig)
	}
	// Note: Transport.Clone may lazily initialize shared internal state on
	// the source; only the fetch-specific exported settings must stay
	// untouched on the global.
	if base.DisableCompression != beforeCompression || base.MaxResponseHeaderBytes != beforeHeaders {
		t.Fatal("global http.DefaultTransport was mutated")
	}
}

func TestFetchSurfacesTransportErrorsWithoutRetrying(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	sentinel := errors.New("proxy connect failed")
	h := &harness{stub: func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, sentinel
	}}
	_, err := h.fetcher(t, nil).Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/"})
	if !errors.Is(err, sentinel) && web.CodeOf(err) != web.CodeUnavailable {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("transport calls = %d, want exactly one attempt with no direct fallback", calls.Load())
	}
}

func TestFetchBodyLimitsGzipBombAndSlowBody(t *testing.T) {
	t.Parallel()
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/large":
			return textResponse(request, "text/plain", strings.Repeat("x", 2*1024*1024)), nil
		case "/bomb":
			zeros := make([]byte, 1024*1024)
			compressed := gzipBytes(t, bytes.Repeat(zeros, 20))
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}, "Content-Encoding": []string{"gzip"}},
				Body:       io.NopCloser(bytes.NewReader(compressed)),
				Request:    request,
			}
			return response, nil
		case "/gzip":
			compressed := gzipBytes(t, []byte("compressed ok"))
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}, "Content-Encoding": []string{"gzip"}},
				Body:       io.NopCloser(bytes.NewReader(compressed)),
				Request:    request,
			}
			return response, nil
		case "/slow":
			if err := request.Context().Err(); err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       slowBody{ctx: request.Context()},
				Request:    request,
			}, nil
		case "/endless":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(endlessReader{}),
				Request:    request,
			}, nil
		default:
			return textResponse(request, "text/plain", "ok"), nil
		}
	}}
	small := h.fetcher(t, func(c *Config) { c.MaxBodyBytes = 1024 * 1024 })
	if _, err := small.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/large"}); web.CodeOf(err) != web.CodeResponseTooLarge {
		t.Fatalf("large body: %v", err)
	}
	if _, err := small.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/bomb"}); web.CodeOf(err) != web.CodeResponseTooLarge {
		t.Fatalf("gzip bomb: %v", err)
	}
	if response, err := small.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/gzip"}); err != nil || response.Evidence.Items[0].Text != "compressed ok" {
		t.Fatalf("gzip: %+v %v", response, err)
	}
	quick := h.fetcher(t, func(c *Config) { c.Timeout = 150 * time.Millisecond })
	if _, err := quick.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/slow"}); web.CodeOf(err) != web.CodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow body: %v", err)
	}
	started := time.Now()
	if _, err := quick.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/endless"}); web.CodeOf(err) != web.CodeTimeout && web.CodeOf(err) != web.CodeResponseTooLarge {
		t.Fatalf("endless body: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("endless body was not bounded in time")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.fetcher(t, nil).Fetch(ctx, web.FetchRequest{URL: "http://example.test/slow"}); web.CodeOf(err) != web.CodeCanceled {
		t.Fatalf("canceled: %v", err)
	}
}

func TestFetchTruncatesLongDocumentsAndKeepsUTF8(t *testing.T) {
	t.Parallel()
	h := &harness{stub: func(request *http.Request) (*http.Response, error) {
		return textResponse(request, "text/plain; charset=utf-8", strings.Repeat("汉字", 40*1024)), nil
	}}
	response, err := h.fetcher(t, nil).Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	item := response.Evidence.Items[0]
	if !item.Truncated || len(item.Text) > MaxDocumentBytes || !strings.HasSuffix(item.Text, "字") && !strings.HasSuffix(item.Text, "汉") {
		t.Fatalf("truncation = %v %d", item.Truncated, len(item.Text))
	}
	if err := response.Evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if rendered := web.RenderFetch(response); len(rendered) > web.MaxFetchOutputBytes || !strings.Contains(rendered, "[content truncated") {
		t.Fatalf("rendered %d bytes", len(rendered))
	}
}

func TestOriginOf(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"HTTPS://Example.com:443/x?y#z": "https://example.com",
		"http://example.com:80/":        "http://example.com",
		"http://[2606:4700::1]/":        "http://[2606:4700::1]",
	} {
		if got, err := OriginOf(raw); err != nil || got != want {
			t.Fatalf("OriginOf(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := OriginOf("http://example.com:8443/"); web.CodeOf(err) != web.CodeBlockedTarget {
		t.Fatal(err)
	}
}
