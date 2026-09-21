package web

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/evidence"
)

func TestNormalizeDomainAndBoundaryMatching(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"Example.COM.", "example.com"},
		{"docs.example.com", "docs.example.com"},
		{"例え.jp", "xn--r8jz45g.jp"},
	} {
		got, err := NormalizeDomain(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("NormalizeDomain(%q) = %q, %v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"", "https://example.com", "example.com/docs", "*.example.com", "example.com:443", "a b.com", "-bad-.com"} {
		if _, err := NormalizeDomain(bad); err == nil {
			t.Fatalf("NormalizeDomain(%q) accepted", bad)
		}
	}
	if domainMatches("notexample.com", "example.com") {
		t.Fatal("suffix without label boundary matched")
	}
	if !domainMatches("a.example.com", "example.com") || !domainMatches("example.com", "example.com") {
		t.Fatal("subdomain or exact match failed")
	}
}

func TestCombineDomainsIntersectsAllowUnionsDeny(t *testing.T) {
	t.Parallel()
	policy := DomainPolicy{Allowed: []string{"example.com", "go.dev"}, Excluded: []string{"ads.example.com"}}
	allowed, excluded, err := CombineDomains(policy, SearchRequest{AllowedDomains: []string{"pkg.go.dev", "Example.com"}, ExcludedDomains: []string{"spam.go.dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(allowed, []string{"example.com", "pkg.go.dev"}) {
		t.Fatalf("allowed = %v", allowed)
	}
	if !reflect.DeepEqual(excluded, []string{"ads.example.com", "spam.go.dev"}) {
		t.Fatalf("excluded = %v", excluded)
	}
	_, _, err = CombineDomains(policy, SearchRequest{AllowedDomains: []string{"notexample.com"}})
	if CodeOf(err) != CodeConstraintConflict {
		t.Fatalf("empty intersection must be a conflict, got %v", err)
	}
	allowed, _, err = CombineDomains(DomainPolicy{}, SearchRequest{})
	if err != nil || allowed != nil {
		t.Fatalf("no constraints must stay nil: %v %v", allowed, err)
	}
	allowed, _, err = CombineDomains(DomainPolicy{Allowed: []string{"example.com"}}, SearchRequest{})
	if err != nil || !reflect.DeepEqual(allowed, []string{"example.com"}) {
		t.Fatalf("policy alone = %v %v", allowed, err)
	}
	if _, _, err := CombineDomains(DomainPolicy{Allowed: []string{"bad domain"}}, SearchRequest{}); CodeOf(err) != CodeInvalidConfig {
		t.Fatalf("bad policy = %v", err)
	}
	if _, _, err := CombineDomains(DomainPolicy{}, SearchRequest{AllowedDomains: []string{"http://x"}}); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("bad request = %v", err)
	}
}

func TestResolveTable(t *testing.T) {
	t.Parallel()
	ready := Candidate{Entry: "service:exa-main", InstanceID: "exa-main", ProviderID: "exa", APIID: "exa-rest", EndpointOrigin: "https://api.exa.ai", Availability: AvailabilityReady}
	missing := Candidate{Entry: "service:exa-main", InstanceID: "exa-main", Availability: AvailabilityMissingCredentials, Detail: "credential EXA_API_KEY is not set"}
	invalid := Candidate{Entry: "service:bad", InstanceID: "bad", Availability: AvailabilityInvalidConfig, Detail: "unknown provider"}
	disabled := Candidate{Entry: "service:off", InstanceID: "off", Availability: AvailabilityDisabled, Detail: "disabled"}
	nativeReady := Candidate{Entry: PriorityNative, Availability: AvailabilityReady}
	cases := []struct {
		name      string
		input     ResolveInput
		wantKind  BindingKind
		wantID    string
		wantCode  ErrorCode
		wantSkips int
		wantIn    string
	}{
		{name: "native first not implemented then exa", input: ResolveInput{Enabled: true, Priority: []string{"native", "service:exa-main"}, Candidates: map[string]Candidate{"service:exa-main": ready}}, wantKind: BindingService, wantID: "exa-main", wantSkips: 1},
		{name: "exa first wins over ready native stand-in", input: ResolveInput{Enabled: true, Priority: []string{"service:exa-main", "native"}, Candidates: map[string]Candidate{"service:exa-main": ready, "native": nativeReady}}, wantKind: BindingService, wantID: "exa-main"},
		{name: "only native", input: ResolveInput{Enabled: true, Priority: []string{"native"}}, wantKind: BindingNone, wantSkips: 1, wantIn: "no search source"},
		{name: "empty priority", input: ResolveInput{Enabled: true, Priority: []string{}}, wantKind: BindingNone, wantIn: "empty"},
		{name: "disabled", input: ResolveInput{Enabled: false, Priority: []string{"service:exa-main"}, Candidates: map[string]Candidate{"service:exa-main": ready}}, wantKind: BindingNone, wantIn: "disabled"},
		{name: "unknown instance", input: ResolveInput{Enabled: true, Priority: []string{"service:ghost"}}, wantCode: CodeInvalidConfig},
		{name: "duplicate entry", input: ResolveInput{Enabled: true, Priority: []string{"native", "native"}}, wantCode: CodeInvalidConfig},
		{name: "malformed entry", input: ResolveInput{Enabled: true, Priority: []string{"exa-main"}}, wantCode: CodeInvalidConfig},
		{name: "invalid config is an error", input: ResolveInput{Enabled: true, Priority: []string{"service:bad", "service:exa-main"}, Candidates: map[string]Candidate{"service:bad": invalid, "service:exa-main": ready}}, wantCode: CodeInvalidConfig},
		{name: "missing key does not fall back", input: ResolveInput{Enabled: true, Priority: []string{"service:exa-main", "service:other"}, Candidates: map[string]Candidate{"service:exa-main": missing, "service:other": {Entry: "service:other", InstanceID: "other", Availability: AvailabilityReady}}}, wantKind: BindingNone, wantSkips: 1, wantIn: "EXA_API_KEY"},
		{name: "disabled instance skipped", input: ResolveInput{Enabled: true, Priority: []string{"service:off", "service:exa-main"}, Candidates: map[string]Candidate{"service:off": disabled, "service:exa-main": ready}}, wantKind: BindingService, wantID: "exa-main", wantSkips: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binding, err := Resolve(tc.input)
			if tc.wantCode != "" {
				if CodeOf(err) != tc.wantCode {
					t.Fatalf("err = %v, want code %s", err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if binding.Kind != tc.wantKind || binding.Selected.InstanceID != tc.wantID {
				t.Fatalf("binding = %+v", binding)
			}
			if len(binding.Skipped) != tc.wantSkips {
				t.Fatalf("skipped = %+v", binding.Skipped)
			}
			if tc.wantIn != "" && !strings.Contains(binding.Reason, tc.wantIn) {
				t.Fatalf("reason %q lacks %q", binding.Reason, tc.wantIn)
			}
		})
	}
}

func TestSearchRequestValidate(t *testing.T) {
	t.Parallel()
	if err := (SearchRequest{Query: "  "}).Validate(); CodeOf(err) != CodeInvalidArgument {
		t.Fatal(err)
	}
	if err := (SearchRequest{Query: strings.Repeat("字", MaxQueryBytes)}).Validate(); CodeOf(err) != CodeInvalidArgument {
		t.Fatal("byte limit must apply to UTF-8 bytes, not characters")
	}
	if err := (SearchRequest{Query: "ok", MaxResults: 21}).Validate(); CodeOf(err) != CodeInvalidArgument {
		t.Fatal(err)
	}
	if err := (SearchRequest{Query: "ok", MaxResults: 0}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (FetchRequest{URL: "https://example.com", Format: "html"}).Validate(); CodeOf(err) != CodeInvalidArgument {
		t.Fatal(err)
	}
}

func TestRenderSearchIsDeterministicAndSanitized(t *testing.T) {
	t.Parallel()
	source, _ := evidence.NewSource("https://example.com/docs", "Docs \x1b[31mRED\x1b[0m\u202e title", "2024-01-02")
	other, _ := evidence.NewSource("https://go.dev/blog", "", "")
	response := SearchResponse{Query: "go context\x1b]8;;http://evil\x07", Evidence: evidence.Bundle{
		Sources: []evidence.Source{source, other},
		Items: []evidence.Evidence{
			{SourceID: source.ID, Kind: evidence.KindExcerpt, Text: "excerpt one", Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 1},
			{SourceID: other.ID, Kind: evidence.KindSummary, Text: strings.Repeat("长", 3000), Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 2},
		},
		Diagnostics: evidence.Diagnostics{UpstreamRequestID: "req-123", DurationMS: 55},
	}}
	first := RenderSearch(response)
	response.Evidence.Diagnostics.UpstreamRequestID = "req-456"
	response.Evidence.Items[0].RetrievedAt = 999
	second := RenderSearch(response)
	if first != second {
		t.Fatal("operational fields changed model-facing output")
	}
	for _, forbidden := range []string{"\x1b", "\u202e", "req-123", "\x07"} {
		if strings.Contains(first, forbidden) {
			t.Fatalf("output contains %q: %q", forbidden, first)
		}
	}
	for _, want := range []string{"[1] Docs RED title", "URL: https://example.com/docs", "Published: 2024-01-02T00:00:00Z", "Evidence (excerpt): excerpt one", "[2] (untitled)", "Evidence (summary):", "[…]", searchFooter} {
		if !strings.Contains(first, want) {
			t.Fatalf("output lacks %q:\n%s", want, first)
		}
	}
	if len(first) > MaxSearchOutputBytes {
		t.Fatalf("output %d bytes exceeds limit", len(first))
	}
	if empty := RenderSearch(SearchResponse{Query: "q"}); !strings.Contains(empty, "No results.") {
		t.Fatalf("empty render = %q", empty)
	}
}

func TestRenderSearchReservesSpaceForLaterEntries(t *testing.T) {
	t.Parallel()
	var sources []evidence.Source
	var items []evidence.Evidence
	for i := range 40 {
		source, _ := evidence.NewSource("https://example.com/"+strings.Repeat("p", 10)+string(rune('a'+i%26))+string(rune('a'+i/26)), "T", "")
		sources = append(sources, source)
		items = append(items, evidence.Evidence{SourceID: source.ID, Kind: evidence.KindExcerpt, Text: strings.Repeat("e", MaxResultEvidenceBytes), Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 1})
	}
	out := RenderSearch(SearchResponse{Query: "q", Evidence: evidence.Bundle{Sources: sources, Items: items}})
	if len(out) > MaxSearchOutputBytes {
		t.Fatalf("output %d exceeds limit", len(out))
	}
	if !strings.Contains(out, truncatedResultsNotice) || !strings.HasSuffix(out, searchFooter) {
		t.Fatal("truncation notice or footer missing")
	}
	// Every emitted entry must be complete: a URL line follows each header.
	if strings.Count(out, "\n[")-1 != strings.Count(out, "\nURL: ") {
		t.Fatal("an entry was cut between title and URL")
	}
}

func TestRenderFetchBoundsAndMarks(t *testing.T) {
	t.Parallel()
	source, _ := evidence.NewSource("https://example.com/a", "Page", "")
	response := FetchResponse{RequestedURL: "https://example.com/a", FinalURL: "https://example.com/b", HTTPStatus: 200, MediaType: "text/html", Extraction: "article", Evidence: evidence.Bundle{
		Sources: []evidence.Source{source},
		Items:   []evidence.Evidence{{SourceID: source.ID, Kind: evidence.KindDocument, Text: strings.Repeat("字", 40*1024), Format: evidence.FormatMarkdown, Acquisition: evidence.AcquisitionHTTPFetch, RetrievedAt: 1}},
	}}
	out := RenderFetch(response)
	if len(out) > MaxFetchOutputBytes {
		t.Fatalf("output %d exceeds limit", len(out))
	}
	if !strings.Contains(out, "Final URL: https://example.com/b") || !strings.Contains(out, "Title: Page") || !strings.Contains(out, "Extraction: article") || !strings.Contains(out, "[content truncated") {
		t.Fatalf("fetch output = %q", out[:200])
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "]") {
		t.Fatal("truncation marker must be last")
	}
}

func TestErrorClassificationRetainsCause(t *testing.T) {
	t.Parallel()
	err := WrapContextError(context.Canceled, CodeUnavailable, "x")
	if err.Code != CodeCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("%+v", err)
	}
	err = WrapContextError(context.DeadlineExceeded, CodeUnavailable, "x")
	if err.Code != CodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%+v", err)
	}
	plain := errors.New("boom")
	if got := WrapContextError(plain, CodeUnavailable, "safe"); got.Code != CodeUnavailable || got.Error() != "unavailable: safe" || !errors.Is(got, plain) {
		t.Fatalf("%+v", got)
	}
	if CodeOf(errors.New("other")) != "" {
		t.Fatal("unclassified error has a code")
	}
}
