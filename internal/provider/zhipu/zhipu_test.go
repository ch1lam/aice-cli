package zhipu_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/zhipu"
)

func TestStreamAndToolReplay(t *testing.T) {
	t.Parallel()
	for _, level := range []llm.ThinkingLevel{llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax} {
		t.Run(string(level), func(t *testing.T) {
			var bodies []map[string]any
			service, err := zhipu.New(zhipu.Config{APIKey: "test-key", HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://open.bigmodel.cn/api/paas/v4/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("wrong endpoint or authorization: %s", r.URL)
				}
				if !strings.HasPrefix(r.Header.Get("User-Agent"), "aice/") {
					t.Error("missing AICE identity")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				bodies = append(bodies, body)
				if body["model"] != "glm-5.3" || body["stream"] != true || body["reasoning_effort"] != string(level) || body["thinking"].(map[string]any)["type"] != "enabled" {
					t.Errorf("request controls = %#v", body)
				}
				if body["max_tokens"] != float64(131072) {
					t.Error("incorrect output budget")
				}
				events := []string{
					`{"choices":[{"delta":{"reasoning_content":"Read the file."}}]}`,
					`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
					`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
					`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2}}}`,
					`[DONE]`,
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + strings.Join(events, "\n\ndata: ") + "\n\n")), Request: r}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			request := apitest.MinimalRequest(zhipu.DefaultModel().API)
			request.Model = zhipu.DefaultModel()
			request.Options.Thinking = level
			request.Tools = []llm.ToolDefinition{{Name: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}}
			stream, err := service.Stream(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			events := apitest.CollectEvents(t, stream)
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			done := events[len(events)-1]
			if done.Type != llm.EventTypeDone || done.StopReason != llm.StopReasonToolUse || done.Message == nil {
				t.Fatalf("done = %#v", done)
			}
			if done.Message.Provider != zhipu.ProviderID || done.Message.Usage.TotalTokens != 15 || len(done.Message.Content) != 2 {
				t.Fatalf("message = %#v", done.Message)
			}
			request.Messages = append(request.Messages, *done.Message, llm.ToolResultMessage{Role: llm.RoleToolResult, ToolCallID: "call-1", Content: []llm.ContentPart{llm.NewTextContent("file contents").Part()}})
			stream, err = service.Stream(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			apitest.CollectEvents(t, stream)
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			messages := bodies[1]["messages"].([]any)
			assistant := messages[1].(map[string]any)
			call := assistant["tool_calls"].([]any)[0].(map[string]any)
			result := messages[2].(map[string]any)
			if assistant["content"] != "" || assistant["reasoning_content"] != "Read the file." || call["id"] != "call-1" || result["tool_call_id"] != "call-1" || result["content"] != "file contents" {
				t.Fatalf("replay = %#v", messages)
			}
		})
	}
}

func TestRejectsIncompatibleRequestsBeforeHTTP(t *testing.T) {
	t.Parallel()
	service, err := zhipu.New(zhipu.Config{APIKey: "test-key", HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected network call")
		return nil, io.ErrUnexpectedEOF
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*llm.Request){
		"provider":     func(r *llm.Request) { r.Model.Provider = "openai" },
		"model":        func(r *llm.Request) { r.Model.ID = "unknown" },
		"protocol":     func(r *llm.Request) { r.Model.API = "openai-responses" },
		"thinking off": func(r *llm.Request) { r.Options.Thinking = llm.ThinkingLevelOff },
		"image": func(r *llm.Request) {
			r.Messages = []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := apitest.MinimalRequest(zhipu.DefaultModel().API)
			r.Model = zhipu.DefaultModel()
			mutate(&r)
			if _, err := service.Stream(t.Context(), r); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestConnectionValidation(t *testing.T) {
	t.Parallel()
	for _, c := range []zhipu.Config{{}, {APIKey: "key", BaseURL: "file:///tmp/api"}, {APIKey: "key", BaseURL: "https://"}} {
		if _, err := zhipu.New(c); err == nil {
			t.Fatal("accepted invalid connection")
		}
	}
	models := zhipu.Models()
	models[0].ThinkingLevelMap[llm.ThinkingLevelHigh] = nil
	if !zhipu.DefaultModel().ThinkingLevelMap.Supports(llm.ThinkingLevelHigh) {
		t.Fatal("catalog shares mutable map")
	}
}
