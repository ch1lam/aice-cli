package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/web"
)

type fakeSearchBackend struct {
	capabilities web.SearchCapabilities
	requests     []web.SearchRequest
	response     web.SearchResponse
	err          error
}

func (f *fakeSearchBackend) Capabilities() web.SearchCapabilities { return f.capabilities }

func (f *fakeSearchBackend) Search(_ context.Context, request web.SearchRequest) (web.SearchResponse, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return web.SearchResponse{}, f.err
	}
	response := f.response
	response.Query = request.Query
	return response, nil
}

func fakeBundle(t *testing.T, url, title, text string) evidence.Bundle {
	t.Helper()
	source, err := evidence.NewSource(url, title, "")
	if err != nil {
		t.Fatal(err)
	}
	return evidence.Bundle{Sources: []evidence.Source{source}, Items: []evidence.Evidence{{
		SourceID: source.ID, Kind: evidence.KindExcerpt, Text: text, Format: evidence.FormatText,
		Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 1, ReturnedBytes: len(text),
	}}}
}

func TestWebSearchCombinesPolicyAndAttachesEvidence(t *testing.T) {
	t.Parallel()
	backend := &fakeSearchBackend{capabilities: web.SearchCapabilities{AllowedDomains: true, ExcludedDomains: true}, response: web.SearchResponse{InstanceID: "fake-1", Evidence: fakeBundle(t, "https://pkg.go.dev/context", "context package", "cancellation excerpt")}}
	search, err := NewWebSearch(WebSearchOptions{Backend: backend, Label: "Fake / fake-1", Policy: web.DomainPolicy{Allowed: []string{"go.dev"}, Excluded: []string{"spam.go.dev"}}, DefaultMaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	definition := search.Definition()
	if definition.Name != "web_search" || !strings.Contains(definition.Description, "Fake / fake-1") || len(definition.PromptGuidelines) == 0 {
		t.Fatalf("definition = %+v", definition)
	}
	call := llm.ToolCall{ID: "c1", Name: "web_search", Arguments: json.RawMessage(`{"query":"go context","allowed_domains":["pkg.go.dev"]}`)}
	result, err := search.Execute(t.Context(), call)
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 1 {
		t.Fatalf("requests = %d", len(backend.requests))
	}
	got := backend.requests[0]
	if got.Query != "go context" || got.MaxResults != 5 || !reflect.DeepEqual(got.AllowedDomains, []string{"pkg.go.dev"}) || !reflect.DeepEqual(got.ExcludedDomains, []string{"spam.go.dev"}) {
		t.Fatalf("backend request = %+v", got)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "[1] context package") || !strings.Contains(text, "URL: https://pkg.go.dev/context") || !strings.Contains(text, "cancellation excerpt") {
		t.Fatalf("content = %q", text)
	}
	if result.Evidence == nil || len(result.Evidence.Sources) != 1 || result.IsError {
		t.Fatalf("evidence = %+v", result.Evidence)
	}
	message, err := llm.NewToolResultMessage(result)
	if err != nil || message.Evidence == nil {
		t.Fatalf("message = %+v %v", message, err)
	}

	// Model domains outside the policy are a conflict; nothing is sent.
	_, err = search.Execute(t.Context(), llm.ToolCall{ID: "c2", Name: "web_search", Arguments: json.RawMessage(`{"query":"x","allowed_domains":["example.com"]}`)})
	if web.CodeOf(err) != web.CodeConstraintConflict || len(backend.requests) != 1 {
		t.Fatalf("conflict = %v (requests %d)", err, len(backend.requests))
	}
	for _, arguments := range []string{`{}`, `{"query":""}`, `{"query":"x","max_results":21}`, `{"query":"x","unknown":1}`, `{"query":"x","allowed_domains":["http://x"]}`} {
		if _, err := search.Execute(t.Context(), llm.ToolCall{ID: "c3", Name: "web_search", Arguments: json.RawMessage(arguments)}); err == nil {
			t.Fatalf("arguments %s accepted", arguments)
		}
	}
	if len(backend.requests) != 1 {
		t.Fatal("invalid arguments reached the backend")
	}

	// Backend errors surface with their classification and no partial result.
	backend.err = web.NewError(web.CodeRateLimited, "slow down")
	_, err = search.Execute(t.Context(), call)
	if web.CodeOf(err) != web.CodeRateLimited {
		t.Fatalf("err = %v", err)
	}
	backend.err = context.Canceled
	if _, err := search.Execute(t.Context(), call); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}

	// Empty results are a successful result without an evidence object.
	backend.err = nil
	backend.response = web.SearchResponse{InstanceID: "fake-1"}
	result, err = search.Execute(t.Context(), call)
	if err != nil || result.Evidence != nil || !strings.Contains(result.Content[0].Text, "No results.") {
		t.Fatalf("empty = %+v %v", result, err)
	}
}

func TestWebSearchRefusesConstraintsTheBackendCannotEnforce(t *testing.T) {
	t.Parallel()
	backend := &fakeSearchBackend{capabilities: web.SearchCapabilities{}}
	search, err := NewWebSearch(WebSearchOptions{Backend: backend, Policy: web.DomainPolicy{Excluded: []string{"bad.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = search.Execute(t.Context(), llm.ToolCall{ID: "c1", Name: "web_search", Arguments: json.RawMessage(`{"query":"x"}`)})
	if web.CodeOf(err) != web.CodeUnsupportedConstraint || len(backend.requests) != 0 {
		t.Fatalf("err = %v requests = %d", err, len(backend.requests))
	}
	search, _ = NewWebSearch(WebSearchOptions{Backend: backend})
	_, err = search.Execute(t.Context(), llm.ToolCall{ID: "c1", Name: "web_search", Arguments: json.RawMessage(`{"query":"x","allowed_domains":["go.dev"]}`)})
	if web.CodeOf(err) != web.CodeUnsupportedConstraint {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewWebSearch(WebSearchOptions{}); err == nil {
		t.Fatal("nil backend accepted")
	}
	if _, err := NewWebSearch(WebSearchOptions{Backend: backend, DefaultMaxResults: 50}); err == nil {
		t.Fatal("invalid default accepted")
	}
}

type fakeFetchBackend struct {
	requests []web.FetchRequest
	response web.FetchResponse
	err      error
}

func (f *fakeFetchBackend) Fetch(_ context.Context, request web.FetchRequest) (web.FetchResponse, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return web.FetchResponse{}, f.err
	}
	return f.response, nil
}

func TestWebFetchValidatesAndAttachesEvidence(t *testing.T) {
	t.Parallel()
	bundle := fakeBundle(t, "https://example.com/docs", "Docs", "# Title\n\nbody")
	bundle.Items[0].Kind, bundle.Items[0].Format, bundle.Items[0].Acquisition = evidence.KindDocument, evidence.FormatMarkdown, evidence.AcquisitionHTTPFetch
	backend := &fakeFetchBackend{response: web.FetchResponse{RequestedURL: "https://example.com/docs", FinalURL: "https://example.com/docs", HTTPStatus: 200, MediaType: "text/html", Extraction: "main", Evidence: bundle}}
	fetch, err := NewWebFetch(backend)
	if err != nil {
		t.Fatal(err)
	}
	if fetch.Definition().Name != "web_fetch" {
		t.Fatal(fetch.Definition())
	}
	result, err := fetch.Execute(t.Context(), llm.ToolCall{ID: "f1", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com/docs","format":"text"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 1 || backend.requests[0].Format != web.FetchFormatText {
		t.Fatalf("requests = %+v", backend.requests)
	}
	if text := result.Content[0].Text; !strings.Contains(text, "Title: Docs") || !strings.Contains(text, "# Title") || !strings.Contains(text, "Extraction: main") {
		t.Fatalf("content = %q", text)
	}
	if result.Evidence == nil || result.Evidence.Items[0].Kind != evidence.KindDocument {
		t.Fatalf("evidence = %+v", result.Evidence)
	}
	for _, arguments := range []string{`{}`, `{"url":"ftp://x"}`, `{"url":"https://example.com","format":"html"}`, `{"url":"https://example.com","headers":{}}`, `{"url":"` + strings.Repeat("a", 9000) + `"}`} {
		if _, err := fetch.Execute(t.Context(), llm.ToolCall{ID: "f2", Name: "web_fetch", Arguments: json.RawMessage(arguments)}); err == nil {
			t.Fatalf("arguments %s accepted", arguments)
		}
	}
	if len(backend.requests) != 1 {
		t.Fatal("invalid arguments reached the backend")
	}
	backend.err = web.NewError(web.CodeBlockedTarget, "private")
	if _, err := fetch.Execute(t.Context(), llm.ToolCall{ID: "f3", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com"}`)}); web.CodeOf(err) != web.CodeBlockedTarget {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewWebFetch(nil); err == nil {
		t.Fatal("nil backend accepted")
	}
}
