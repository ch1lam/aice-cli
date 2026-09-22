package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/web"
)

// fakeWireBackend simulates a second search service whose native response
// shape (rows of "headline"/"link"/"blurb") differs from Exa. It normalizes
// into the shared evidence contract; the tool, display and Session code must
// work with it unchanged.
type fakeWireBackend struct {
	calls atomic.Int32
	rows  []fakeWireRow
	err   error
}

type fakeWireRow struct {
	Headline, Link, Blurb, When string
}

func (f *fakeWireBackend) Capabilities() web.SearchCapabilities {
	return web.SearchCapabilities{AllowedDomains: true, ExcludedDomains: true}
}

func (f *fakeWireBackend) Search(_ context.Context, request web.SearchRequest) (web.SearchResponse, error) {
	f.calls.Add(1)
	if f.err != nil {
		return web.SearchResponse{}, f.err
	}
	bundle := evidence.Bundle{}
	for _, row := range f.rows {
		source, err := evidence.NewSource(row.Link, row.Headline, row.When)
		if err != nil {
			bundle.Diagnostics.Warnings = append(bundle.Diagnostics.Warnings, err.Error())
			continue
		}
		bundle.Sources = append(bundle.Sources, source)
		bundle.Items = append(bundle.Items, evidence.Evidence{SourceID: source.ID, Kind: evidence.KindSnippet, Text: row.Blurb, Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 42, ReturnedBytes: len(row.Blurb)})
	}
	return web.SearchResponse{InstanceID: "fake-main", ProviderID: "fake", APIID: "fake-wire", Query: request.Query, Evidence: bundle}, nil
}

type fakeFetch struct {
	calls atomic.Int32
}

func (f *fakeFetch) Fetch(_ context.Context, request web.FetchRequest) (web.FetchResponse, error) {
	f.calls.Add(1)
	source, _ := evidence.NewSource(request.URL, "Fetched page", "")
	return web.FetchResponse{RequestedURL: request.URL, FinalURL: source.URL, HTTPStatus: 200, MediaType: "text/html", Extraction: "main", Evidence: evidence.Bundle{
		Sources: []evidence.Source{source},
		Items:   []evidence.Evidence{{SourceID: source.ID, Kind: evidence.KindDocument, Text: "# Page\n\nfetched body", Format: evidence.FormatMarkdown, Acquisition: evidence.AcquisitionHTTPFetch, RetrievedAt: 7, ReturnedBytes: 20}},
	}}, nil
}

func testWebBackends(search *fakeWireBackend, fetch *fakeFetch) *webBackends {
	return &webBackends{
		search: map[string]webSearchFactory{
			"fake": fakeSearchFactory(search, "fake-wire"),
		},
		newFetcher: func(config.WebConfig) (web.FetchBackend, error) { return fetch, nil },
	}
}

// fakeSearchFactory stands in for a provider adapter: it validates the api
// name like a real descriptor and never touches the network.
func fakeSearchFactory(search *fakeWireBackend, api string) webSearchFactory {
	return func(service config.WebService, _ config.WebConfig) (webSearchService, error) {
		if service.API != "" && service.API != api {
			return webSearchService{}, errors.New("api " + service.API + " is not supported by provider " + service.Provider)
		}
		return webSearchService{backend: search, origin: "https://fake.example", label: "Fake / " + service.ID}, nil
	}
}

func readyWebConfig(priority ...string) config.WebConfig {
	return config.WebConfig{
		SearchEnabled: true, Priority: priority, DefaultMaxResults: 8, SearchTimeout: 25 * time.Second,
		FetchEnabled: true, FetchTimeout: 30 * time.Second,
		Services: map[string]config.WebService{
			"fake-main": {ID: "fake-main", Provider: "fake", Enabled: true, Secret: "k", SecretPresent: true, Credential: config.WebCredentialRef{Env: "FAKE_KEY"}},
		},
	}
}

func TestBindWebRoutingTable(t *testing.T) {
	t.Parallel()
	backends := testWebBackends(&fakeWireBackend{}, &fakeFetch{})
	missing := readyWebConfig("service:fake-main")
	service := missing.Services["fake-main"]
	service.Secret, service.SecretPresent = "", false
	missing.Services["fake-main"] = service
	unknownProvider := readyWebConfig("service:fake-main")
	service = unknownProvider.Services["fake-main"]
	service.Provider = "nobody"
	unknownProvider.Services["fake-main"] = service
	badAPI := readyWebConfig("service:fake-main")
	service = badAPI.Services["fake-main"]
	service.API = "other"
	badAPI.Services["fake-main"] = service
	disabledFetch := readyWebConfig("service:fake-main")
	disabledFetch.FetchEnabled = false
	searchOff := readyWebConfig("service:fake-main")
	searchOff.SearchEnabled = false

	cases := []struct {
		name      string
		settings  config.WebConfig
		wantTools []string
		wantErr   bool
		wantIn    string
	}{
		{"zero config keeps coding tools only", config.WebConfig{}, nil, false, "disabled"},
		{"native first then ready service", readyWebConfig("native", "service:fake-main"), []string{"web_search", "web_fetch"}, false, ""},
		{"service first", readyWebConfig("service:fake-main", "native"), []string{"web_search", "web_fetch"}, false, ""},
		{"native only", readyWebConfig("native"), []string{"web_fetch"}, false, "no search source"},
		{"empty priority", readyWebConfig(), []string{"web_fetch"}, false, "empty"},
		{"search disabled", searchOff, []string{"web_fetch"}, false, "disabled"},
		{"missing credential", missing, []string{"web_fetch"}, false, "FAKE_KEY"},
		{"unknown provider", unknownProvider, []string{"web_fetch"}, true, "not supported"},
		{"unknown api", badAPI, []string{"web_fetch"}, true, "not supported"},
		{"unknown instance", func() config.WebConfig { c := readyWebConfig("service:ghost"); return c }(), []string{"web_fetch"}, true, "ghost"},
		{"fetch disabled", disabledFetch, []string{"web_search"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := bindWeb(*backends, tc.settings)
			if got := toolNames(state.tools()); !reflect.DeepEqual(got, tc.wantTools) && !(len(got) == 0 && len(tc.wantTools) == 0) {
				t.Fatalf("tools = %v, want %v", got, tc.wantTools)
			}
			if (state.configErr != nil) != tc.wantErr {
				t.Fatalf("configErr = %v", state.configErr)
			}
			status := strings.Join(state.statusLines(), "\n")
			if tc.wantIn != "" && !strings.Contains(status, tc.wantIn) {
				t.Fatalf("status lacks %q:\n%s", tc.wantIn, status)
			}
			if state.searchTool != nil && state.searchTarget != "search:fake-main@https://fake.example" {
				t.Fatalf("search target = %q", state.searchTarget)
			}
			if state.searchTool == nil && state.searchTarget != "" {
				t.Fatal("target set without a bound tool")
			}
		})
	}
	// Native stays visible as not implemented even when a service is chosen.
	state := bindWeb(*backends, readyWebConfig("native", "service:fake-main"))
	status := strings.Join(state.statusLines(), "\n")
	if !strings.Contains(status, "skipped native: not_implemented") || !strings.Contains(status, "Search source: service:fake-main (fake, https://fake.example)") {
		t.Fatalf("status = %s", status)
	}
}

func runWebPrint(t *testing.T, settings config.WebConfig, backends *webBackends, call llm.ToolCall, yolo bool, sessionPath string) (*toolLoopModel, error) {
	t.Helper()
	model := &toolLoopModel{firstCall: &call}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) {
			return config.Config{DeepSeekAPIKey: "test-key", Web: settings}, nil
		},
		newModel:    func(config.Config) (agent.Model, error) { return model, nil },
		webBackends: backends,
	})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--workspace", t.TempDir(), "--print", "look it up"}
	if yolo {
		args = append(args, "--yolo")
	}
	if sessionPath != "" {
		args = append(args, "--session", sessionPath)
	}
	command.SetOut(io.Discard)
	command.SetArgs(args)
	return model, command.ExecuteContext(t.Context())
}

func lastToolResult(t *testing.T, model *toolLoopModel) llm.ToolResultMessage {
	t.Helper()
	if len(model.requests) < 2 {
		t.Fatalf("model requests = %d", len(model.requests))
	}
	messages := model.requests[1].Messages
	result, ok := messages[len(messages)-1].(llm.ToolResultMessage)
	if !ok {
		t.Fatalf("last message = %T", messages[len(messages)-1])
	}
	return result
}

func TestWebSearchThroughPrintWithFakeBackend(t *testing.T) {
	t.Parallel()
	search := &fakeWireBackend{rows: []fakeWireRow{
		{Headline: "Go blog \x1b[31mred\x1b[0m", Link: "HTTPS://Go.dev/blog/context", Blurb: "Context cancellation explained 中文", When: "2024-05-01"},
		{Headline: "bad", Link: "javascript:alert(1)"},
	}}
	fetch := &fakeFetch{}
	sessionPath := filepath.Join(t.TempDir(), "web.jsonl")
	call := llm.ToolCall{ID: "call-1", Name: "web_search", Arguments: json.RawMessage(`{"query":"go context cancellation","max_results":3}`)}
	// --print uses the same web policy as interactive mode: no --yolo needed.
	model, err := runWebPrint(t, readyWebConfig("native", "service:fake-main"), testWebBackends(search, fetch), call, false, sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if search.calls.Load() != 1 {
		t.Fatalf("backend calls = %d", search.calls.Load())
	}
	names := make([]string, 0)
	for _, definition := range model.requests[0].Tools {
		names = append(names, definition.Name)
	}
	if !reflect.DeepEqual(names, []string{"read", "write", "edit", "bash", "grep", "find", "ls", "skill", "web_search", "web_fetch"}) {
		t.Fatalf("tools = %v", names)
	}
	if !strings.Contains(model.requests[0].SystemPrompt, "web_search:") || !strings.Contains(model.requests[0].SystemPrompt, "web_fetch:") {
		t.Fatal("system prompt does not list the web tools")
	}
	result := lastToolResult(t, model)
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
	text := result.Content[0].Text
	for _, want := range []string{"[1] Go blog red", "URL: https://go.dev/blog/context", "Published: 2024-05-01T00:00:00Z", "Evidence (snippet): Context cancellation explained 中文"} {
		if !strings.Contains(text, want) {
			t.Fatalf("content lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") || strings.Contains(text, "javascript") {
		t.Fatalf("unsafe content in model text: %q", text)
	}
	if result.Evidence == nil || len(result.Evidence.Sources) != 1 || len(result.Evidence.Diagnostics.Warnings) != 1 {
		t.Fatalf("evidence = %+v", result.Evidence)
	}

	// Persisted, replayed and projected without provider knowledge.
	store, err := session.Open(t.Context(), sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := sessionTranscript(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range transcript.Entries {
		if entry.Kind != interaction.TranscriptTool || entry.Tool.Name != "web_search" {
			continue
		}
		found = true
		if entry.Tool.Detail != "go context cancellation" || entry.Tool.Evidence == nil || len(entry.Tool.Evidence.Sources) != 1 {
			t.Fatalf("restored tool = %+v", entry.Tool)
		}
		source := entry.Tool.Evidence.Sources[0]
		if source.URL != "https://go.dev/blog/context" || !reflect.DeepEqual(source.Kinds, []string{"snippet"}) {
			t.Fatalf("restored source = %+v", source)
		}
	}
	if !found {
		t.Fatal("web_search entry missing from restored transcript")
	}
	// The wire request to the model carries content only, never the evidence object.
	encoded, err := json.Marshal(model.requests[1].Messages[len(model.requests[1].Messages)-1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "evidence") {
		t.Fatal("transcript message must retain evidence for history")
	}
	converted, err := llm.AgentMessagesToMessages([]llm.AgentMessage{result})
	if err != nil || len(converted) != 1 {
		t.Fatal(err)
	}
}

func TestWebToolsDefaultAllowWithoutApproval(t *testing.T) {
	t.Parallel()
	search := &fakeWireBackend{rows: []fakeWireRow{{Headline: "x", Link: "https://example.com/", Blurb: "b"}}}
	fetch := &fakeFetch{}
	backends := testWebBackends(search, fetch)
	settings := readyWebConfig("service:fake-main")

	// Legal search runs in --print without --yolo and without any approval.
	model, err := runWebPrint(t, settings, backends, llm.ToolCall{ID: "call-1", Name: "web_search", Arguments: json.RawMessage(`{"query":"secret plans"}`)}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	result := lastToolResult(t, model)
	if result.IsError || result.Evidence == nil {
		t.Fatalf("default search = %+v", result)
	}
	if search.calls.Load() != 1 {
		t.Fatalf("search calls = %d, want 1", search.calls.Load())
	}

	// Legal fetches from different normal domains also run without approval.
	for _, url := range []string{"https://example.com/page", "https://other.example/b"} {
		model, err := runWebPrint(t, settings, backends, llm.ToolCall{ID: "call-1", Name: "web_fetch", Arguments: json.RawMessage(`{"url":` + strconv.Quote(url) + `}`)}, false, "")
		if err != nil {
			t.Fatal(err)
		}
		result := lastToolResult(t, model)
		if result.IsError || result.Evidence == nil || !strings.Contains(result.Content[0].Text, "fetched body") {
			t.Fatalf("default fetch %s = %+v", url, result)
		}
	}
	if fetch.calls.Load() != 2 {
		t.Fatalf("fetch calls = %d, want 2", fetch.calls.Load())
	}

	// A URL outside the shared shape policy is refused before any backend
	// call, with or without --yolo. Address policy for private targets is
	// exercised in the httpfetch package.
	for _, yolo := range []bool{false, true} {
		before := fetch.calls.Load()
		model, err := runWebPrint(t, settings, backends, llm.ToolCall{ID: "call-1", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com:8443/admin"}`)}, yolo, "")
		if err != nil {
			t.Fatal(err)
		}
		result := lastToolResult(t, model)
		if !result.IsError || fetch.calls.Load() != before || !strings.Contains(result.Content[0].Text, "port") {
			t.Fatalf("yolo=%v illegal fetch = %+v (fetch calls %d)", yolo, result, fetch.calls.Load())
		}
		if result.Evidence != nil {
			t.Fatal("denied call recorded evidence")
		}
	}

	// Disabled fetch registers no tool and sends no request.
	disabledFetch := settings
	disabledFetch.FetchEnabled = false
	before := fetch.calls.Load()
	model, err = runWebPrint(t, disabledFetch, backends, llm.ToolCall{ID: "call-1", Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com/page"}`)}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	result = lastToolResult(t, model)
	if !result.IsError || fetch.calls.Load() != before {
		t.Fatalf("disabled fetch = %+v (fetch calls %d)", result, fetch.calls.Load())
	}

	// Without a bound search source the schema is absent and a call denies
	// without contacting any backend.
	model, err = runWebPrint(t, readyWebConfig("native"), backends, llm.ToolCall{ID: "call-1", Name: "web_search", Arguments: json.RawMessage(`{"query":"x"}`)}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range model.requests[0].Tools {
		if definition.Name == "web_search" {
			t.Fatal("web_search registered without a source")
		}
	}
	result = lastToolResult(t, model)
	if !result.IsError || search.calls.Load() != 1 {
		t.Fatalf("unbound web_search = %+v", result)
	}
}

func TestWebGuardAllowsWithoutSessionGrant(t *testing.T) {
	t.Parallel()
	sess := newGuardAskSession(t, t.TempDir(), guard.Config{})
	sess.guard.SetSearchTarget("search:fake-main@https://fake.example")
	call := llm.ToolCall{ID: "call-1", Name: "web_search", Arguments: json.RawMessage(`{"query":"x"}`)}
	if res, err := sess.guard.Check(t.Context(), call); err != nil || res.Decision != guard.DecisionAllow || len(res.Approvals) != 0 {
		t.Fatalf("search = %+v %v", res, err)
	}
	// Rebinding and clearing session grants never introduces a confirmation.
	sess.guard.SetSearchTarget("search:fake-main@https://other.example")
	if res, _ := sess.guard.Check(t.Context(), call); res.Decision != guard.DecisionAllow {
		t.Fatalf("rebound search = %+v", res)
	}
	sess.guard.SetSearchTarget("search:fake-main@https://fake.example")
	sess.guard.ResetSessionGrants()
	if res, _ := sess.guard.Check(t.Context(), call); res.Decision != guard.DecisionAllow {
		t.Fatalf("search after reset = %+v", res)
	}

	for _, url := range []string{"https://docs.example/a", "https://docs.example/b", "https://other.example/b"} {
		fetchCall := llm.ToolCall{ID: "call-2", Name: "web_fetch", Arguments: json.RawMessage(`{"url":` + strconv.Quote(url) + `}`)}
		if res, _ := sess.guard.Check(t.Context(), fetchCall); res.Decision != guard.DecisionAllow || len(res.Approvals) != 0 {
			t.Fatalf("fetch %s = %+v", url, res)
		}
	}
}

func TestDisplayToolEvidenceProjection(t *testing.T) {
	t.Parallel()
	if displayToolEvidence(nil) != nil || displayToolEvidence(&llm.ToolResultMessage{}) != nil {
		t.Fatal("absent evidence must project to nil")
	}
	source, _ := evidence.NewSource("https://example.com/a", "A", "")
	message := &llm.ToolResultMessage{Evidence: &evidence.Bundle{
		Sources: []evidence.Source{source},
		Items: []evidence.Evidence{
			{SourceID: source.ID, Kind: evidence.KindExcerpt, Text: "e", Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 1},
			{SourceID: source.ID, Kind: evidence.KindExcerpt, Text: "f", Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 1},
			{SourceID: source.ID, Kind: evidence.KindSummary, Text: "s", Format: evidence.FormatText, Acquisition: evidence.AcquisitionSearchService, RetrievedAt: 1},
		},
		Diagnostics: evidence.Diagnostics{Warnings: []string{"dropped one"}},
	}}
	display := displayToolEvidence(message)
	if display == nil || len(display.Sources) != 1 || !reflect.DeepEqual(display.Sources[0].Kinds, []string{"excerpt", "summary"}) || display.Sources[0].Title != "A" || display.Warnings[0] != "dropped one" {
		t.Fatalf("display = %+v", display)
	}
	display.Warnings[0] = "mutated"
	if message.Evidence.Diagnostics.Warnings[0] != "dropped one" {
		t.Fatal("projection aliases the message")
	}
	if detail := toolCallDetail(llm.ToolCall{Name: "web_fetch", Arguments: json.RawMessage(`{"url":"https://x.example/"}`)}); detail != "https://x.example/" {
		t.Fatalf("fetch detail = %q", detail)
	}
}
