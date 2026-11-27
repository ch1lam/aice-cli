package openairesponses_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
)

// webSearchServer records each request body and streams events.
func webSearchServer(t *testing.T, events []string) (*openairesponses.Adapter, chan map[string]any) {
	t.Helper()
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = io.WriteString(w, "data: "+event+"\n\n")
		}
	}))
	t.Cleanup(server.Close)
	adapter, err := openairesponses.New(openairesponses.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, requests
}

func streamAll(t *testing.T, adapter *openairesponses.Adapter, request llm.Request) ([]llm.Event, llm.AssistantMessage) {
	t.Helper()
	modelStream, err := adapter.Stream(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer modelStream.Close()
	var events []llm.Event
	for {
		event, err := modelStream.Next()
		if errors.Is(err, io.EOF) {
			t.Fatal("stream ended without a terminal event")
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
		if event.Type == llm.EventTypeDone || event.Type == llm.EventTypeError {
			if event.Type == llm.EventTypeError {
				t.Fatalf("stream error: %v", event.Err)
			}
			return events, *event.Message
		}
	}
}

const deepseekSearchCall = `{"type":"web_search_call","id":"srvtoolu_ds_00","status":"completed","kind":"deep_search","action":{"type":"search","queries":["latest go"],"sources":[{"type":"url","url":"https://go.dev/doc/devel/release","title":"Release History"}]}}`

func TestAdapterDeclaresDeepSeekSearchAndRecordsSteps(t *testing.T) {
	t.Parallel()
	adapter, requests := webSearchServer(t, []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp-1","model":"deepseek-flash","status":"in_progress"}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"web_search_call","id":"srvtoolu_ds_00","status":"in_progress","action":{"type":"search"}}}`,
		`{"type":"response.web_search_call.searching","sequence_number":2,"output_index":0,"item_id":"srvtoolu_ds_00"}`,
		`{"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":` + deepseekSearchCall + `}`,
		`{"type":"response.output_item.done","sequence_number":4,"output_index":1,"item":{"type":"web_search_call","id":"srvtoolu_ds_01","status":"failed","action":{"type":"open_page","url":"https://go.dev/dl/"},"error":{"code":"unavailable"}}}`,
		`{"type":"response.output_item.added","sequence_number":5,"output_index":2,"item":{"id":"msg-1","type":"message","role":"assistant","content":[],"status":"in_progress"}}`,
		`{"type":"response.output_text.delta","sequence_number":6,"output_index":2,"content_index":0,"item_id":"msg-1","delta":"Go 1.27.4","logprobs":[]}`,
		`{"type":"response.output_item.done","sequence_number":7,"output_index":2,"item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Go 1.27.4","annotations":[{"type":"url_citation","url":"https://go.dev","title":"Go","start_index":0,"end_index":9}]}],"status":"completed"}}`,
		`{"type":"response.completed","sequence_number":8,"response":{"id":"resp-1","model":"deepseek-flash","status":"completed"}}`,
	})
	request := llm.Request{
		Model:     deepseekModelForTest(t, "deepseek-flash"),
		Messages:  []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("latest go?").Part()}}},
		WebSearch: &llm.WebSearch{Replaces: "web_search"},
		Options:   llm.StreamOptions{MaxTokens: 256, Thinking: llm.ThinkingLevelHigh},
	}
	events, message := streamAll(t, adapter, request)

	body := <-requests
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "deepseek_web_search" {
		t.Fatalf("tools = %#v", body["tools"])
	}
	if _, ok := body["include"]; ok {
		t.Fatalf("include sent for DeepSeek: %#v", body["include"])
	}

	var searchEvents int
	for _, event := range events {
		if event.Type == llm.EventTypeWebSearch {
			searchEvents++
		}
	}
	if searchEvents != 2 || len(message.Content) != 3 || message.StopReason != llm.StopReasonStop {
		t.Fatalf("events = %d content = %#v stop = %s", searchEvents, message.Content, message.StopReason)
	}
	first, failed := message.Content[0].WebSearch, message.Content[1].WebSearch
	if first.ID != "srvtoolu_ds_00" || first.Queries[0] != "latest go" || first.Sources[0].Title != "Release History" || string(first.Replay[0]) != deepseekSearchCall {
		t.Fatalf("first = %+v", first)
	}
	if failed.Action != llm.WebSearchActionOpenPage || failed.URL != "https://go.dev/dl/" || failed.Error != "unavailable" {
		t.Fatalf("failed = %+v", failed)
	}
	if err := message.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterReplaysSearchStepsOnlyToSameModelWithSearch(t *testing.T) {
	t.Parallel()
	done := []string{`{"type":"response.completed","sequence_number":0,"response":{"id":"r","model":"m","status":"completed"}}`}
	flash := deepseekModelForTest(t, "deepseek-flash")
	history := func(model llm.Model) []llm.Message {
		return []llm.Message{
			llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("q").Part()}},
			llm.AssistantMessage{Role: llm.RoleAssistant, API: model.API, Provider: model.Provider, ModelID: model.ID, StopReason: llm.StopReasonStop,
				Content: []llm.ContentPart{
					llm.NewWebSearchPart(llm.WebSearchCall{ID: "srvtoolu_ds_00", Replay: []json.RawMessage{json.RawMessage(deepseekSearchCall)}}),
					llm.NewTextContent("answer").Part(),
				}},
			llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("more").Part()}},
		}
	}
	for _, tc := range []struct {
		name     string
		producer llm.Model
		search   *llm.WebSearch
		replayed bool
	}{
		{"same model with search", flash, &llm.WebSearch{}, true},
		{"same model without search", flash, nil, false},
		{"other model", llm.Model{ID: "other", API: openairesponses.API, Provider: "deepseek"}, &llm.WebSearch{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter, requests := webSearchServer(t, done)
			request := llm.Request{Model: flash, Messages: history(tc.producer), WebSearch: tc.search,
				Options: llm.StreamOptions{MaxTokens: 64, Thinking: llm.ThinkingLevelHigh}}
			streamAll(t, adapter, request)
			input := (<-requests)["input"].([]any)
			var found map[string]any
			for _, item := range input {
				if object := item.(map[string]any); object["type"] == "web_search_call" {
					found = object
				}
			}
			if (found != nil) != tc.replayed {
				t.Fatalf("replayed = %v, input = %#v", found != nil, input)
			}
			// DeepSeek's own fields travel back unchanged.
			if found != nil && found["kind"] != "deep_search" {
				t.Fatalf("replayed item = %#v", found)
			}
		})
	}
}

func TestAdapterDeclaresOpenAIWebSearchWithDomainFilterAndSources(t *testing.T) {
	t.Parallel()
	adapter, requests := webSearchServer(t, []string{`{"type":"response.completed","sequence_number":0,"response":{"id":"r","model":"m","status":"completed"}}`})
	request := llm.Request{
		Model:     openai.DefaultModel(),
		Messages:  []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("q").Part()}}},
		WebSearch: &llm.WebSearch{AllowedDomains: []string{"go.dev"}},
		Options:   llm.StreamOptions{MaxTokens: 64},
	}
	streamAll(t, adapter, request)
	body := <-requests
	tool := body["tools"].([]any)[0].(map[string]any)
	filters, _ := tool["filters"].(map[string]any)
	if tool["type"] != "web_search" || filters == nil || filters["allowed_domains"].([]any)[0] != "go.dev" {
		t.Fatalf("tool = %#v", tool)
	}
	include, _ := body["include"].([]any)
	if len(include) != 1 || include[0] != "web_search_call.action.sources" {
		t.Fatalf("include = %#v", body["include"])
	}
}

func TestAdapterRenamesToolsDeepSeekSearchReserves(t *testing.T) {
	t.Parallel()
	adapter, requests := webSearchServer(t, []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"r","model":"deepseek-flash","status":"in_progress"}}`,
		`{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"id":"fc-2","type":"function_call","call_id":"call-2","name":"aice_find","arguments":"{}","status":"completed"}}`,
		`{"type":"response.completed","sequence_number":2,"response":{"id":"r","model":"deepseek-flash","status":"completed"}}`,
	})
	flash := deepseekModelForTest(t, "deepseek-flash")
	schema := json.RawMessage(`{"type":"object"}`)
	request := llm.Request{
		Model: flash,
		Messages: []llm.Message{
			llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("q").Part()}},
			llm.AssistantMessage{Role: llm.RoleAssistant, API: flash.API, Provider: flash.Provider, ModelID: flash.ID, StopReason: llm.StopReasonToolUse,
				Content: []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "call-1", Name: "find", Arguments: json.RawMessage(`{}`)}}}},
			llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call-1", ToolName: "find", Content: []llm.ContentPart{llm.NewTextContent("a.go").Part()}},
		},
		Tools:     []llm.ToolDefinition{{Name: "find", InputSchema: schema}, {Name: "aice_find", InputSchema: schema}, {Name: "read", InputSchema: schema}},
		WebSearch: &llm.WebSearch{},
		Options:   llm.StreamOptions{MaxTokens: 64, Thinking: llm.ThinkingLevelHigh},
	}
	_, message := streamAll(t, adapter, request)
	body := <-requests
	var names []string
	for _, tool := range body["tools"].([]any) {
		if name, ok := tool.(map[string]any)["name"].(string); ok {
			names = append(names, name)
		}
	}
	if len(names) != 3 || names[0] != "aice_find_" || names[1] != "aice_find" || names[2] != "read" {
		t.Fatalf("wire tools = %v", names)
	}
	if call := body["input"].([]any)[1].(map[string]any); call["name"] != "aice_find_" {
		t.Fatalf("history call = %#v", call)
	}
	// aice_find is the request's own tool, not a renamed one.
	if got := message.Content[0].ToolCall.Name; got != "aice_find" {
		t.Fatalf("streamed call name = %q", got)
	}
}
