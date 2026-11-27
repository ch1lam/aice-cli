package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropicapi "github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// searchServer records each request body and streams events.
func searchServer(t *testing.T, events []string) (*anthropicapi.Adapter, chan map[string]any) {
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
			var head struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(event), &head)
			_, _ = io.WriteString(w, "event: "+head.Type+"\ndata: "+event+"\n\n")
		}
	}))
	t.Cleanup(server.Close)
	adapter, err := anthropicapi.New(anthropicapi.Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, requests
}

func streamMessage(t *testing.T, adapter *anthropicapi.Adapter, request llm.Request) ([]llm.Event, llm.AssistantMessage) {
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
		switch event.Type {
		case llm.EventTypeError:
			t.Fatalf("stream error: %v", event.Err)
		case llm.EventTypeDone:
			return events, *event.Message
		}
	}
}

const (
	searchStart  = `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{},"caller":{"type":"direct"}}}`
	searchResult = `{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","title":"Release History","url":"https://go.dev/doc/devel/release","encrypted_content":"opaque","page_age":null}]}`
)

func searchEvents(stopReason string) []string {
	return []string{
		`{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"deepseek-v4-pro","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
		searchStart,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"latest go\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":` + searchResult + `}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","url":"https://go.dev/doc/devel/release","title":"Release History","encrypted_index":"x","cited_text":"go1.27.4"}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Go 1.27.4"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"` + stopReason + `","stop_sequence":null},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
}

func TestAdapterGroupsServerSearchWithItsResult(t *testing.T) {
	t.Parallel()
	adapter, requests := searchServer(t, searchEvents("end_turn"))
	model := anthropicModelForTest(t, "deepseek-v4-pro")
	request := llm.Request{
		Model:     model,
		Messages:  []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("latest go?").Part()}}},
		WebSearch: &llm.WebSearch{Replaces: "web_search"},
		Options:   llm.StreamOptions{MaxTokens: 256},
	}
	events, message := streamMessage(t, adapter, request)
	body := <-requests
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search_20250305" || tools[0].(map[string]any)["name"] != "web_search" {
		t.Fatalf("tools = %#v", body["tools"])
	}

	var searched int
	for _, event := range events {
		if event.Type == llm.EventTypeWebSearch {
			searched++
		}
	}
	if searched != 1 || len(message.Content) != 2 || message.Content[1].Text != "Go 1.27.4" {
		t.Fatalf("events = %d content = %#v", searched, message.Content)
	}
	call := message.Content[0].WebSearch
	if call.ID != "srvtoolu_1" || call.Queries[0] != "latest go" || len(call.Sources) != 1 || len(call.Replay) != 2 {
		t.Fatalf("call = %+v", call)
	}
	if !strings.Contains(string(call.Replay[0]), `"caller"`) || !strings.Contains(string(call.Replay[0]), `"query":"latest go"`) || string(call.Replay[1]) != searchResult {
		t.Fatalf("replay = %s", call.Replay)
	}

	// The next request returns the blocks unchanged to the same model.
	adapter, requests = searchServer(t, searchEvents("end_turn"))
	request.Messages = append(request.Messages, message,
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("thanks").Part()}})
	streamMessage(t, adapter, request)
	assistant := (<-requests)["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if len(assistant) != 3 || assistant[0].(map[string]any)["type"] != "server_tool_use" ||
		assistant[1].(map[string]any)["type"] != "web_search_tool_result" || assistant[1].(map[string]any)["content"].([]any)[0].(map[string]any)["encrypted_content"] != "opaque" {
		t.Fatalf("replayed assistant = %#v", assistant)
	}

	// Without the server tool declared, the steps are dropped.
	adapter, requests = searchServer(t, searchEvents("end_turn"))
	request.WebSearch = nil
	streamMessage(t, adapter, request)
	assistant = (<-requests)["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if len(assistant) != 1 || assistant[0].(map[string]any)["type"] != "text" {
		t.Fatalf("dropped assistant = %#v", assistant)
	}
}

func TestAdapterMapsPausedServerTurn(t *testing.T) {
	t.Parallel()
	adapter, _ := searchServer(t, searchEvents("pause_turn"))
	request := llm.Request{
		Model:     anthropicModelForTest(t, "deepseek-v4-pro"),
		Messages:  []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("q").Part()}}},
		WebSearch: &llm.WebSearch{},
		Options:   llm.StreamOptions{MaxTokens: 256},
	}
	_, message := streamMessage(t, adapter, request)
	if message.StopReason != llm.StopReasonPause {
		t.Fatalf("stop = %s", message.StopReason)
	}
}
