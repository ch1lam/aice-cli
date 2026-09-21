package httpfetch

import (
	"compress/gzip"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/web"
)

// harness routes public-looking hostnames to a local test server without real
// DNS or network: the resolver answers a public address and the dialer connects
// to the server instead. Address validation still runs on the resolver answer.
type harness struct {
	server *httptest.Server
	dials  atomic.Int32
	hosts  map[string][]netip.Addr
}

func newHarness(server *httptest.Server) *harness {
	return &harness{server: server, hosts: map[string][]netip.Addr{
		"example.test":       {netip.MustParseAddr("93.184.216.34")},
		"other.test":         {netip.MustParseAddr("93.184.216.35")},
		"example.com":        {netip.MustParseAddr("93.184.216.34")},
		"mixed.test":         {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.5")},
		"private.test":       {netip.MustParseAddr("192.168.1.1")},
		"loopback.test":      {netip.MustParseAddr("127.0.0.1")},
		"mapped.test":        {netip.MustParseAddr("::ffff:10.1.2.3")},
		"cgnat.test":         {netip.MustParseAddr("100.64.0.1")},
		"linklocal6.test":    {netip.MustParseAddr("fe80::1")},
		"uniquelocal6.test":  {netip.MustParseAddr("fd00::1")},
		"multicast.test":     {netip.MustParseAddr("224.0.0.1")},
		"metadata.test":      {netip.MustParseAddr("169.254.169.254")},
		"nat64.test":         {netip.MustParseAddr("64:ff9b::a00:1")},
		"unspecified.test":   {netip.MustParseAddr("0.0.0.0")},
		"documentation.test": {netip.MustParseAddr("2001:db8::1")},
	}}
}

func (h *harness) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	addrs, ok := h.hosts[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return addrs, nil
}

func (h *harness) dial(ctx context.Context, network, address string) (net.Conn, error) {
	h.dials.Add(1)
	addr, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, err
	}
	if err := checkPublicAddress(addr.Addr()); err != nil {
		return nil, errors.New("dialer received a non-public address: " + err.Error())
	}
	return (&net.Dialer{}).DialContext(ctx, network, h.server.Listener.Addr().String())
}

func (h *harness) fetcher(t *testing.T, mutate func(*Config)) *Fetcher {
	t.Helper()
	cfg := Config{Resolver: h.resolve, Dialer: h.dial, Timeout: 5 * time.Second, Now: func() time.Time { return time.UnixMilli(1700000000000) }}
	if mutate != nil {
		mutate(&cfg)
	}
	fetcher, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return fetcher
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		gotHost, gotAgent, gotEncoding = r.Host, r.Header.Get("User-Agent"), r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, samplePage)
	}))
	defer server.Close()
	h := newHarness(server)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pages[r.URL.Path]
		if page.contentType != "" {
			w.Header().Set("Content-Type", page.contentType)
		} else {
			w.Header()["Content-Type"] = nil // suppress net/http's automatic sniffing
		}
		switch r.URL.Path {
		case "/brotli":
			w.Header().Set("Content-Encoding", "br")
		case "/notfound":
			w.WriteHeader(404)
		case "/auth":
			w.WriteHeader(401)
		case "/rate":
			w.WriteHeader(429)
		case "/server":
			w.WriteHeader(503)
		}
		_, _ = io.WriteString(w, page.body)
	}))
	defer server.Close()
	fetcher := newHarness(server).fetcher(t, nil)
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

func TestFetchRefusesBlockedTargetsWithoutDialing(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "should not be reached") }))
	defer server.Close()
	h := newHarness(server)
	fetcher := h.fetcher(t, nil)
	cases := []struct {
		url      string
		wantCode web.ErrorCode
	}{
		{"http://127.0.0.1/", web.CodeBlockedTarget},
		{"http://localhost/", web.CodeUnavailable}, // unknown to the test resolver; never dialed
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
		{"http://private.test/", web.CodeBlockedTarget},
		{"http://loopback.test/", web.CodeBlockedTarget},
		{"http://mapped.test/", web.CodeBlockedTarget},
		{"http://cgnat.test/", web.CodeBlockedTarget},
		{"http://linklocal6.test/", web.CodeBlockedTarget},
		{"http://uniquelocal6.test/", web.CodeBlockedTarget},
		{"http://multicast.test/", web.CodeBlockedTarget},
		{"http://metadata.test/", web.CodeBlockedTarget},
		{"http://nat64.test/", web.CodeBlockedTarget},
		{"http://unspecified.test/", web.CodeBlockedTarget},
		{"http://documentation.test/", web.CodeBlockedTarget},
		{"http://mixed.test/", web.CodeBlockedTarget},
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
	if h.dials.Load() != 0 {
		t.Fatalf("blocked targets were dialed %d times", h.dials.Load())
	}
}

func TestFetchRedirectPolicy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/relative":
			w.Header().Set("Location", "sub/final")
			w.WriteHeader(http.StatusMovedPermanently)
		case "/sub/final", "/final":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "arrived at "+r.URL.Path)
		case "/cross":
			http.Redirect(w, r, "http://other.test/final", http.StatusFound)
		case "/private":
			http.Redirect(w, r, "http://private.test/final", http.StatusFound)
		case "/literal":
			http.Redirect(w, r, "http://127.0.0.1:80/final", http.StatusFound)
		case "/port":
			http.Redirect(w, r, "http://example.test:8080/final", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/nolocation":
			w.WriteHeader(http.StatusFound)
		case "/scheme":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	h := newHarness(server)
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
	for path, want := range map[string]web.ErrorCode{
		"/cross": web.CodeRedirectRefused, "/private": web.CodeRedirectRefused, "/literal": web.CodeRedirectRefused,
		"/port": web.CodeRedirectRefused, "/loop": web.CodeRedirectRefused, "/nolocation": web.CodeInvalidResponse, "/scheme": web.CodeRedirectRefused,
	} {
		_, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test" + path})
		if web.CodeOf(err) != want {
			t.Fatalf("%s: err = %v, want %s", path, err, want)
		}
		if path == "/cross" && !strings.Contains(err.Error(), "http://other.test/final") {
			t.Fatalf("cross-origin refusal must name the target: %v", err)
		}
	}
	// Allowing cross-origin redirects still validates the target address.
	permissive := h.fetcher(t, func(c *Config) { c.AllowCrossOriginRedirects = true })
	if response, err := permissive.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/cross"}); err != nil || response.FinalURL != "http://other.test/final" {
		t.Fatalf("permissive cross-origin: %+v %v", response, err)
	}
	if _, err := permissive.Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/private"}); web.CodeOf(err) != web.CodeBlockedTarget {
		t.Fatalf("private redirect accepted under permissive policy: %v", err)
	}
}

func TestFetchHTTPSDowngradeAndCertificateHostname(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/downgrade" {
			http.Redirect(w, r, "http://example.com/final", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "secure "+r.Host)
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	h := newHarness(server)
	fetcher := h.fetcher(t, func(c *Config) { c.RootCAs = roots })
	// The httptest certificate is valid for example.com; dialing the pinned
	// address still verifies that hostname.
	response, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "https://example.com/page"})
	if err != nil || response.Evidence.Items[0].Text != "secure example.com" {
		t.Fatalf("tls fetch: %+v %v", response, err)
	}
	_, err = fetcher.Fetch(t.Context(), web.FetchRequest{URL: "https://other.test/page"})
	var certErr x509.HostnameError
	if err == nil || !errors.As(err, &certErr) {
		t.Fatalf("certificate for another hostname accepted: %v", err)
	}
	if _, err := fetcher.Fetch(t.Context(), web.FetchRequest{URL: "https://example.com/downgrade"}); web.CodeOf(err) != web.CodeRedirectRefused || !strings.Contains(err.Error(), "https to http") {
		t.Fatalf("downgrade accepted: %v", err)
	}
}

func TestFetchBodyLimitsGzipBombAndSlowBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		switch r.URL.Path {
		case "/large":
			for range 200 {
				if _, err := io.WriteString(w, strings.Repeat("x", 64*1024)); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		case "/bomb":
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			zeros := make([]byte, 1024*1024)
			for range 20 {
				_, _ = writer.Write(zeros)
			}
			_ = writer.Close()
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			_, _ = io.WriteString(writer, "compressed ok")
			_ = writer.Close()
		case "/slow":
			_, _ = io.WriteString(w, "partial")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/endless":
			for {
				if _, err := io.WriteString(w, "chunk\n"); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				time.Sleep(time.Millisecond)
			}
		}
	}))
	defer server.Close()
	h := newHarness(server)
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

func TestFetchIgnoresProxyEnvironment(t *testing.T) {
	proxyHits := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case proxyHits <- struct{}{}:
		default:
		}
		w.WriteHeader(502)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "direct")
	}))
	defer server.Close()
	response, err := newHarness(server).fetcher(t, nil).Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/"})
	if err != nil || response.Evidence.Items[0].Text != "direct" {
		t.Fatalf("direct fetch: %+v %v", response, err)
	}
	select {
	case <-proxyHits:
		t.Fatal("fetch used the proxy from the environment")
	default:
	}
}

func TestFetchTruncatesLongDocumentsAndKeepsUTF8(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, strings.Repeat("汉字", 40*1024))
	}))
	defer server.Close()
	response, err := newHarness(server).fetcher(t, nil).Fetch(t.Context(), web.FetchRequest{URL: "http://example.test/"})
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
