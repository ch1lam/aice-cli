package anthropic_test

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	anthropicapi "github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
)

func TestSubscriptionWireToolsThinkingAndReplay(t *testing.T) {
	// The SDK must never mix ambient API credentials into explicit OAuth requests.
	t.Setenv("ANTHROPIC_API_KEY", "ambient-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://ambient.invalid")
	var bodies []map[string]any
	client := &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.anthropic.com/v1/messages?beta=true" || r.Header.Get("Authorization") != "Bearer oauth-token" || r.Header.Get("X-Api-Key") != "" {
			t.Error("OAuth endpoint/authentication mismatch")
		}
		if r.Header.Get("User-Agent") != "claude-cli/2.1.280" || r.Header.Get("X-App") != "cli" || !strings.Contains(r.Header.Get("Anthropic-Beta"), "oauth-2025-04-20") {
			t.Error("missing subscription compatibility headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		bodies = append(bodies, body)
		frames := []string{
			`{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan","signature":"signature"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call","name":"Read","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`,
		}
		var data strings.Builder
		for _, frame := range frames {
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(frame), &event)
			data.WriteString("event: " + event.Type + "\ndata: " + frame + "\n\n")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data.String())), Request: r}, nil
	})}
	adapter, err := anthropicapi.New(anthropicapi.Config{OAuthToken: "oauth-token", BaseURL: "https://api.anthropic.com", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	request := apitest.MinimalRequest(anthropicapi.API)
	request.Model = claudesubscription.DefaultModel()
	request.SystemPrompt = "AICE instructions"
	request.Tools = []llm.ToolDefinition{{Name: "read", Description: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "custom_tool", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	before, _ := json.Marshal(request)
	stream, err := adapter.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	events := apitest.CollectEvents(t, stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	var assistant *llm.AssistantMessage
	for _, event := range events {
		if event.ToolCall != nil && event.ToolCall.Name != "read" {
			t.Fatalf("wire name leaked into event: %s", event.ToolCall.Name)
		}
		if event.Type == llm.EventTypeDone {
			assistant = event.Message
		}
	}
	if assistant == nil || assistant.Provider != claudesubscription.ProviderID || assistant.StopReason != llm.StopReasonToolUse || assistant.Content[0].Signature != "signature" || assistant.Content[1].ToolCall.Name != "read" {
		t.Fatalf("incorrect assistant: %+v", assistant)
	}
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("request was mutated")
	}
	system := bodies[0]["system"].([]any)
	if len(system) != 2 || system[0].(map[string]any)["text"] != "You are Claude Code, Anthropic's official CLI for Claude." || system[1].(map[string]any)["text"] != "AICE instructions" {
		t.Fatal("system blocks missing or merged")
	}
	tools := bodies[0]["tools"].([]any)
	if tools[0].(map[string]any)["name"] != "Read" || tools[1].(map[string]any)["name"] != "custom_tool" {
		t.Fatal("incorrect tool mapping")
	}
	request.Messages = append(request.Messages, *assistant, llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call", ToolName: "read", Content: []llm.ContentPart{llm.NewTextContent("contents").Part()}})
	before, _ = json.Marshal(request)
	stream, err = adapter.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	replay, _ := json.Marshal(bodies[1]["messages"])
	for _, expected := range []string{`"name":"Read"`, `"signature":"signature"`, `"tool_use_id":"call"`, `contents`} {
		if !strings.Contains(string(replay), expected) {
			t.Errorf("replay omitted %s", expected)
		}
	}
	after, _ = json.Marshal(request)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("replay mutated Session history")
	}
}

func TestSubscriptionRejectsAmbiguousToolNamesBeforeHTTP(t *testing.T) {
	t.Parallel()
	adapter, err := anthropicapi.New(anthropicapi.Config{OAuthToken: "token", BaseURL: "https://example.test", HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("sent ambiguous tools")
		return nil, io.ErrUnexpectedEOF
	})}})
	if err != nil {
		t.Fatal(err)
	}
	request := apitest.MinimalRequest(anthropicapi.API)
	request.Tools = []llm.ToolDefinition{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if _, err := adapter.Stream(t.Context(), request); err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("collision error = %v", err)
	}
	if _, err := anthropicapi.New(anthropicapi.Config{APIKey: "key", OAuthToken: "token", BaseURL: "https://example.test"}); err == nil {
		t.Fatal("accepted mixed authentication")
	}
}
