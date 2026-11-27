package deepseek

import (
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

// fixtureResponse follows the documented response shape (checked 2026-11-27).
const fixtureResponse = `{"results":[
  {"title":"Go 1.26 Release Notes","site_name":"The Go Programming Language","site_icon":"https://cdn.deepseek.com/site-icons/go.dev",
   "authority":3,"rerank_score":2.5,"date":"2026-02-10T00:00:00+00:00","url":"https://go.dev/doc/go1.26",
   "snippet":"short snippet","summary":"The passage a model should read.","content":""},
  {"title":"No summary","url":"https://example.com/a","snippet":"only a snippet","summary":"","date":null},
  {"title":"bad row","url":"ftp://example.com/x","summary":"ignored"}
]}`

func newTestClient(t *testing.T, server *httptest.Server, key string) *Client {
	t.Helper()
	client, err := New(Config{InstanceID: "deepseek-main", BaseURL: server.URL, APIKey: key, Timeout: 5 * time.Second,
		Transport: server.Client().Transport, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestSearchSendsExactRequestAndNormalizes(t *testing.T) {
	t.Parallel()
	var got struct {
		method, path, auth, agent string
		body                      map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth, got.agent = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		_, _ = io.WriteString(w, fixtureResponse)
	}))
	defer server.Close()
	response, err := newTestClient(t, server, "test-key").Search(t.Context(), web.SearchRequest{
		Query: " Go release notes ", MaxResults: 5, AllowedDomains: []string{"go.dev", "github.com"}, ExcludedDomains: []string{"spam.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/v1/tools/search" || got.auth != "Bearer test-key" || !strings.HasPrefix(got.agent, "aice/") {
		t.Fatalf("request = %+v", got)
	}
	want := map[string]any{"query": "Go release notes site:go.dev OR site:github.com -site:spam.example", "count": float64(5)}
	if body, _ := json.Marshal(got.body); string(body) != string(mustJSON(want)) {
		t.Fatalf("body = %s", body)
	}
	if response.InstanceID != "deepseek-main" || response.ProviderID != ProviderID || response.APIID != APIID || response.Query != "Go release notes" {
		t.Fatalf("identity = %+v", response)
	}
	bundle := response.Evidence
	if len(bundle.Sources) != 2 || bundle.Sources[0].PublishedAt != "2026-02-10T00:00:00Z" || bundle.Sources[1].PublishedAt != "" {
		t.Fatalf("sources = %+v", bundle.Sources)
	}
	if len(bundle.Items) != 2 || bundle.Items[0].Text != "The passage a model should read." || bundle.Items[1].Text != "only a snippet" {
		t.Fatalf("items = %+v", bundle.Items)
	}
	for _, item := range bundle.Items {
		if item.Kind != evidence.KindExcerpt || item.Acquisition != evidence.AcquisitionSearchService || item.RetrievedAt != 1700000000000 {
			t.Fatalf("item = %+v", item)
		}
	}
	if len(bundle.Diagnostics.Warnings) != 1 || !strings.Contains(bundle.Diagnostics.Warnings[0], "result 2 dropped") || bundle.Diagnostics.ReportedCost != nil {
		t.Fatalf("diagnostics = %+v", bundle.Diagnostics)
	}
}

func TestQueryOperatorsCountTowardTheLimit(t *testing.T) {
	t.Parallel()
	if query, err := Query(web.SearchRequest{Query: "q", AllowedDomains: []string{"go.dev"}}); err != nil || query != "q site:go.dev" {
		t.Fatalf("query = %q, %v", query, err)
	}
	long := strings.Repeat("字", MaxQueryRunes-len(" site:go.dev")+1)
	if _, err := Query(web.SearchRequest{Query: long, AllowedDomains: []string{"go.dev"}}); web.CodeOf(err) != web.CodeInvalidArgument || !strings.Contains(err.Error(), "shorten") {
		t.Fatalf("over-limit query = %v", err)
	}
	if _, err := Query(web.SearchRequest{Query: strings.Repeat("字", MaxQueryRunes)}); err != nil {
		t.Fatalf("limit counts characters, not bytes: %v", err)
	}
}

func TestSearchClassifiesFailuresWithoutRetrying(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status int
		body   string
		want   web.ErrorCode
	}{
		{401, `{"error":{"message":"Authentication Fails","type":"authentication_error","param":null,"code":"invalid_request_error"}}`, web.CodeAuthentication},
		{402, `{"error":{"message":"Insufficient Balance"}}`, web.CodeQuotaExceeded},
		{429, `{"error":{"message":"rate limited"}}`, web.CodeRateLimited},
		{400, `{"error":{"message":"'count' must be between 1 and 50"}}`, web.CodeInvalidArgument},
		{408, ``, web.CodeUnavailable},
		{504, `not json`, web.CodeUnavailable},
		{418, ``, web.CodeInvalidResponse},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server, "super-secret-key").Search(t.Context(), web.SearchRequest{Query: "q"})
			var classified *web.Error
			if !errors.As(err, &classified) || classified.Code != tc.want || strings.Contains(err.Error(), "super-secret-key") {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("request sent %d times", calls.Load())
			}
		})
	}
}

func TestSearchRejectsMalformedResponsesAndMissingKey(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	body := `{"results":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	if _, err := newTestClient(t, server, "").Search(t.Context(), web.SearchRequest{Query: "q"}); web.CodeOf(err) != web.CodeMissingCredentials || calls.Load() != 0 {
		t.Fatalf("missing key: %v (%d calls)", err, calls.Load())
	}
	client := newTestClient(t, server, "k")
	response, err := client.Search(t.Context(), web.SearchRequest{Query: "q"})
	if err != nil || len(response.Evidence.Sources) != 0 {
		t.Fatalf("empty results = %+v, %v", response, err)
	}
	body = `{"data":[]}`
	if _, err := client.Search(t.Context(), web.SearchRequest{Query: "q"}); web.CodeOf(err) != web.CodeInvalidResponse {
		t.Fatalf("missing results field: %v", err)
	}
}

func TestSearchDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("Authorization") != "")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/tools/search", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	if _, err := newTestClient(t, server, "k").Search(t.Context(), web.SearchRequest{Query: "q"}); web.CodeOf(err) != web.CodeInvalidResponse || leaked.Load() {
		t.Fatalf("redirect: %v, leaked=%v", err, leaked.Load())
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	for raw, wantErr := range map[string]bool{``: false, `{}`: false, `{"type":"auto"}`: true, `[]`: true} {
		if err := ParseOptions(json.RawMessage(raw)); (err != nil) != wantErr {
			t.Fatalf("ParseOptions(%s) = %v", raw, err)
		}
	}
	client, err := New(Config{})
	if err != nil || client.Origin() != "https://api.deepseek.com" {
		t.Fatalf("default origin = %v", err)
	}
	if _, err := New(Config{BaseURL: "http://gateway.example"}); web.CodeOf(err) != web.CodeInvalidConfig {
		t.Fatalf("plain http accepted: %v", err)
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
