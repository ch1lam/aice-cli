package exa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/web"
)

const fixtureResponse = `{
  "requestId": "b5947044c4b78efa9552a7c89b306d95",
  "resolvedSearchType": "neural",
  "results": [
    {
      "title": "A Comprehensive Overview of Large Language Models",
      "url": "https://arxiv.org/pdf/2307.06435.pdf",
      "publishedDate": "2023-11-16T01:36:32.547Z",
      "author": "Humza Naveed",
      "id": "https://arxiv.org/abs/2307.06435",
      "image": "https://arxiv.org/pdf/2307.06435.pdf/page_1.png",
      "highlights": ["Such requirements have limited their adoption...", "Second highlight 中文"],
      "highlightScores": [0.46, 0.41],
      "summary": "This overview paper highlights key developments...",
      "unknownFutureField": {"nested": true}
    },
    {
      "title": "Go blog",
      "url": "HTTPS://Go.dev/blog/context",
      "text": "Full page text",
      "highlights": []
    },
    {"title": "bad row", "url": "ftp://example.com/x", "highlights": ["ignored"]},
    {"title": "duplicate", "url": "https://go.dev/blog/context", "highlights": ["dup highlight"]}
  ],
  "costDollars": {"total": 0.007, "search": {"neural": 0.007}},
  "searchTime": 312.4
}`

func newTestClient(t *testing.T, server *httptest.Server, key string) *Client {
	t.Helper()
	client, err := New(Config{InstanceID: "exa-main", BaseURL: server.URL, APIKey: key, Timeout: 5 * time.Second, Transport: server.Client().Transport, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestSearchSendsExactRequestAndNormalizes(t *testing.T) {
	t.Parallel()
	var got struct {
		method, path, key, agent, accept, contentType string
		body                                          map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.key, got.agent, got.accept, got.contentType = r.Header.Get("x-api-key"), r.Header.Get("User-Agent"), r.Header.Get("Accept"), r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fixtureResponse)
	}))
	defer server.Close()
	client := newTestClient(t, server, "test-key")
	if !client.Capabilities().AllowedDomains || !client.Capabilities().ExcludedDomains {
		t.Fatal("capabilities")
	}
	response, err := client.Search(t.Context(), web.SearchRequest{Query: " Go context cancellation ", MaxResults: 5, AllowedDomains: []string{"go.dev"}, ExcludedDomains: []string{"spam.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/search" || got.key != "test-key" || !strings.HasPrefix(got.agent, "aice/") || got.accept != "application/json" || got.contentType != "application/json" {
		t.Fatalf("request = %+v", got)
	}
	wantBody := map[string]any{
		"query": "Go context cancellation", "type": "auto", "numResults": float64(5),
		"includeDomains": []any{"go.dev"}, "excludeDomains": []any{"spam.example"},
		"contents": map[string]any{"text": false, "highlights": true},
	}
	if body, _ := json.Marshal(got.body); string(body) != string(mustJSON(wantBody)) {
		t.Fatalf("body = %s\nwant %s", body, mustJSON(wantBody))
	}
	if response.InstanceID != "exa-main" || response.ProviderID != ProviderID || response.APIID != APIID || response.Query != "Go context cancellation" {
		t.Fatalf("response identity = %+v", response)
	}
	bundle := response.Evidence
	if len(bundle.Sources) != 2 {
		t.Fatalf("sources = %+v", bundle.Sources)
	}
	if bundle.Sources[0].PublishedAt != "2023-11-16T01:36:32Z" || bundle.Sources[1].PublishedAt != "" {
		t.Fatalf("published = %q %q", bundle.Sources[0].PublishedAt, bundle.Sources[1].PublishedAt)
	}
	if bundle.Sources[1].URL != "https://go.dev/blog/context" {
		t.Fatalf("normalized url = %q", bundle.Sources[1].URL)
	}
	kinds := map[evidence.Kind]int{}
	for _, item := range bundle.Items {
		kinds[item.Kind]++
		if item.Acquisition != evidence.AcquisitionSearchService || item.RetrievedAt != 1700000000000 || item.Format != evidence.FormatText {
			t.Fatalf("item = %+v", item)
		}
	}
	if kinds[evidence.KindExcerpt] != 3 || kinds[evidence.KindDocument] != 1 || kinds[evidence.KindSummary] != 1 {
		t.Fatalf("kinds = %v", kinds)
	}
	if len(bundle.Diagnostics.Warnings) != 2 || !strings.Contains(bundle.Diagnostics.Warnings[0], "result 2 dropped") || !strings.Contains(bundle.Diagnostics.Warnings[1], "repeats") {
		t.Fatalf("warnings = %v", bundle.Diagnostics.Warnings)
	}
	if bundle.Diagnostics.UpstreamRequestID != "b5947044c4b78efa9552a7c89b306d95" || bundle.Diagnostics.ReportedCost == nil || bundle.Diagnostics.ReportedCost.Amount != 0.007 || bundle.Diagnostics.ReportedCost.Currency != "USD" {
		t.Fatalf("diagnostics = %+v", bundle.Diagnostics)
	}
	if err := bundle.Validate(); err != nil {
		t.Fatal(err)
	}
	rendered := web.RenderSearch(response)
	if strings.Contains(rendered, "b5947044") || !strings.Contains(rendered, "Second highlight 中文") {
		t.Fatalf("rendered = %q", rendered)
	}
}

func TestSearchEmptyMissingAndMalformedResults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body string
		wantCode   web.ErrorCode
		wantEmpty  bool
	}{
		{"empty array", `{"requestId":"r","results":[]}`, "", true},
		{"missing results", `{"requestId":"r"}`, web.CodeInvalidResponse, false},
		{"null results", `{"requestId":"r","results":null}`, web.CodeInvalidResponse, false},
		{"error envelope with 200", `{"requestId":"r","error":"Invalid request","tag":"INVALID_REQUEST"}`, web.CodeInvalidResponse, false},
		{"broken json", `{"results": [`, web.CodeInvalidResponse, false},
		{"type damage", `{"results": "nope"}`, web.CodeInvalidResponse, false},
		{"all rows malformed", `{"results":[{"url":"javascript:alert(1)"},{"url":"https://user:pw@example.com/"}]}`, web.CodeInvalidResponse, false},
		{"missing cost stays unknown", `{"results":[{"url":"https://example.com/a","highlights":["h"]}]}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer server.Close()
			response, err := newTestClient(t, server, "k").Search(t.Context(), web.SearchRequest{Query: "q"})
			if tc.wantCode != "" {
				if web.CodeOf(err) != tc.wantCode {
					t.Fatalf("err = %v, want %s", err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantEmpty && (len(response.Evidence.Sources) != 0 || len(response.Evidence.Items) != 0) {
				t.Fatalf("expected empty success, got %+v", response.Evidence)
			}
			if !tc.wantEmpty && response.Evidence.Diagnostics.ReportedCost != nil {
				t.Fatal("unknown cost must not become zero")
			}
		})
	}
}

func TestSearchClassifiesHTTPErrorsWithoutLeakingSecrets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status   int
		body     string
		retry    string
		wantCode web.ErrorCode
	}{
		{401, `{"error":"Invalid API key","tag":"INVALID_API_KEY"}`, "", web.CodeAuthentication},
		{403, `{"error":{"message":"forbidden"}}`, "", web.CodeAuthentication},
		{402, `{"error":"Payment required"}`, "", web.CodeQuotaExceeded},
		{429, `{"error":"slow down"}`, "7", web.CodeRateLimited},
		{400, `{"error":"bad body","tag":"INVALID_REQUEST_BODY"}`, "", web.CodeInvalidArgument},
		{500, `not json \x1b[31m`, "", web.CodeUnavailable},
		{503, `{"error":"capacity"}`, "", web.CodeUnavailable},
		{418, ``, "", web.CodeInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if tc.retry != "" {
					w.Header().Set("Retry-After", tc.retry)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server, "super-secret-key").Search(t.Context(), web.SearchRequest{Query: "q"})
			var classified *web.Error
			if !errors.As(err, &classified) || classified.Code != tc.wantCode {
				t.Fatalf("err = %v, want %s", err, tc.wantCode)
			}
			if strings.Contains(err.Error(), "super-secret-key") || strings.Contains(err.Error(), "x-api-key") || strings.Contains(err.Error(), "\x1b") {
				t.Fatalf("error leaks headers or control sequences: %q", err)
			}
			if classified.RetryAfter != tc.retry {
				t.Fatalf("retry-after = %q", classified.RetryAfter)
			}
			if calls.Load() != 1 {
				t.Fatalf("paid request was retried %d times", calls.Load())
			}
		})
	}
}

// countingTransport records how many response body bytes the client actually
// consumes. Measuring on the client side keeps the assertion independent of
// kernel socket buffering, which lets the server write several megabytes
// ahead of what the client has read.
type countingTransport struct {
	next http.RoundTripper
	read atomic.Int64
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := c.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	response.Body = &countingBody{ReadCloser: response.Body, read: &c.read}
	return response, nil
}

type countingBody struct {
	io.ReadCloser
	read *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read.Add(int64(n))
	return n, err
}

func TestSearchBoundsBodiesAndStopsReading(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		_, _ = io.WriteString(w, `{"results":[`)
		flusher := w.(http.Flusher)
		chunk := strings.Repeat("x", 64*1024)
		for range 100 {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	defer server.Close()
	transport := &countingTransport{next: server.Client().Transport}
	client, err := New(Config{InstanceID: "exa-main", BaseURL: server.URL, APIKey: "k", Timeout: 5 * time.Second, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Search(t.Context(), web.SearchRequest{Query: "q"})
	if web.CodeOf(err) != web.CodeResponseTooLarge {
		t.Fatalf("err = %v", err)
	}
	// The client must stop at limit+1; allow one extra read buffer of slack.
	if read := transport.read.Load(); read > int64(maxSuccessBodyBytes)+64*1024 {
		t.Fatalf("client kept reading %d bytes past the %d byte limit", read, maxSuccessBodyBytes)
	}
}

func TestSearchCancellationAndTimeoutRetainCause(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	client := newTestClient(t, server, "k")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := client.Search(ctx, web.SearchRequest{Query: "q"})
		done <- err
	}()
	cancel()
	err := <-done
	if web.CodeOf(err) != web.CodeCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err = %v", err)
	}

	timeoutClient, err := New(Config{InstanceID: "t", BaseURL: server.URL, APIKey: "k", Timeout: 50 * time.Millisecond, Transport: server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = timeoutClient.Search(t.Context(), web.SearchRequest{Query: "q"})
	if web.CodeOf(err) != web.CodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err = %v", err)
	}
}

func TestSearchDoesNotFollowRedirectsOrLeakKey(t *testing.T) {
	t.Parallel()
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" {
			leaked.Store(true)
		}
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/search", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := newTestClient(t, server, "k").Search(t.Context(), web.SearchRequest{Query: "q"})
	if web.CodeOf(err) != web.CodeInvalidResponse || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v", err)
	}
	if leaked.Load() {
		t.Fatal("x-api-key was sent to the redirect target")
	}
}

func TestMissingKeyAndOptions(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	_, err := newTestClient(t, server, "").Search(t.Context(), web.SearchRequest{Query: "q"})
	if web.CodeOf(err) != web.CodeMissingCredentials || calls.Load() != 0 {
		t.Fatalf("missing key: %v (%d calls)", err, calls.Load())
	}
	if _, err := newTestClient(t, server, "k").Search(t.Context(), web.SearchRequest{Query: ""}); web.CodeOf(err) != web.CodeInvalidArgument {
		t.Fatal(err)
	}
	for raw, wantErr := range map[string]bool{
		``: false, `{"type":"auto"}`: false, `{"type":"fast"}`: false, `{}`: false,
		`{"type":"deep"}`: true, `{"type":"neural"}`: true, `{"typo":"auto"}`: true, `{"type":"unknown"}`: true, `[]`: true,
	} {
		options, err := ParseOptions(json.RawMessage(raw))
		if (err != nil) != wantErr {
			t.Fatalf("ParseOptions(%s) = %+v, %v", raw, options, err)
		}
		if err == nil && options.Type == "" {
			t.Fatalf("ParseOptions(%s) left type empty", raw)
		}
	}
	if _, err := New(Config{BaseURL: "http://gateway.example"}); web.CodeOf(err) != web.CodeInvalidConfig {
		t.Fatalf("plain http accepted: %v", err)
	}
	if _, err := New(Config{BaseURL: "http://127.0.0.1:9999/v1/"}); err != nil {
		t.Fatalf("loopback http rejected: %v", err)
	}
	if _, err := New(Config{BaseURL: "https://u:p@api.exa.ai"}); web.CodeOf(err) != web.CodeInvalidConfig {
		t.Fatal("userinfo accepted")
	}
	client, err := New(Config{})
	if err != nil || client.Origin() != "https://api.exa.ai" {
		t.Fatalf("default origin = %q, %v", client.Origin(), err)
	}
	if _, err := New(Config{Options: json.RawMessage(`{"type":"deep"}`)}); web.CodeOf(err) != web.CodeInvalidConfig {
		t.Fatal("deep accepted at construction")
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
