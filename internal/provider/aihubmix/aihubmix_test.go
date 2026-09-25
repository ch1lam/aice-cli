package aihubmix_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/aihubmix"
)

func TestCredentialsAndValidation(t *testing.T) {
	t.Parallel()
	descriptor := &aihubmix.Provider{}
	if descriptor.Configured(config.Config{OpenAIAPIKey: "unrelated", CustomAPIKey: "unrelated"}) {
		t.Fatal("accepted another provider's credential")
	}
	for _, key := range []string{"", "  "} {
		if _, err := aihubmix.New(aihubmix.Config{APIKey: key}); err == nil {
			t.Fatal("accepted blank key")
		}
	}
	configuration := config.Config{}
	descriptor.ApplyAPIKey(&configuration, "fixture-key")
	if !descriptor.Configured(configuration) {
		t.Fatal("applied key is unavailable")
	}
	if !strings.Contains(descriptor.CredentialNotConfiguredError().Error(), "AIHUBMIX_API_KEY") {
		t.Fatal("missing setup guidance")
	}
	client := &http.Client{Transport: apitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("invalid request reached HTTP")
		return nil, io.ErrUnexpectedEOF
	})}
	service, err := aihubmix.New(aihubmix.Config{APIKey: "fixture-key", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*llm.Request)
	}{
		{"provider", func(r *llm.Request) { r.Model.Provider = "openai" }},
		{"model", func(r *llm.Request) { r.Model.ID = "unknown" }},
		{"protocol", func(r *llm.Request) { r.Model.API = anthropic.API }},
		{"unsupported effort", func(r *llm.Request) { r.Options.Thinking = llm.ThinkingLevelMinimal }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := apitest.MinimalRequest(openairesponses.API)
			request.Model = aihubmix.DefaultModel()
			tc.mutate(&request)
			if _, err := service.Stream(t.Context(), request); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
}

func TestCatalogWireAndToolReplay(t *testing.T) {
	t.Parallel()
	for _, model := range aihubmix.Models() {
		for _, level := range llm.SupportedThinkingLevels(model) {
			t.Run(model.ID+"/"+string(level), func(t *testing.T) {
				for _, endpoint := range []string{"", " https://gateway.example/prefix/v1/// "} {
					base := "https://aihubmix.com/v1"
					if endpoint != "" {
						base = "https://gateway.example/prefix/v1"
					}
					calls := 0
					client := &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						suffix := "/chat/completions"
						if model.API == anthropic.API {
							suffix = "/messages"
						}
						if model.API == openairesponses.API {
							suffix = "/responses"
						}
						if r.URL.String() != base+suffix || r.Method != http.MethodPost {
							t.Errorf("route = %s %s", r.Method, r.URL)
						}
						auth := r.Header.Get("Authorization")
						if model.API == anthropic.API {
							auth = "Bearer " + r.Header.Get("X-Api-Key")
						}
						if auth != "Bearer fixture-key" {
							t.Error("wrong credential")
						}
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body["model"] != model.ID || body["stream"] != true {
							t.Fatalf("body = %#v", body)
						}
						assertThinking(t, model, level, body)
						if calls == 2 {
							encoded, err := json.Marshal(body)
							if err != nil {
								t.Fatal(err)
							}
							for _, value := range []string{"call-1", "fixture result", "read"} {
								if !strings.Contains(string(encoded), value) {
									t.Errorf("missing replay %s: %s", value, encoded)
								}
							}
							if model.API == anthropic.API && !strings.Contains(string(encoded), "opaque-signature") {
								t.Error("lost Claude thinking signature")
							}
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}},
							Body: io.NopCloser(strings.NewReader(toolSSE(model.API))), Request: r}, nil
					})}
					service, err := aihubmix.New(aihubmix.Config{APIKey: "fixture-key", BaseURL: endpoint, HTTPClient: client})
					if err != nil {
						t.Fatal(err)
					}
					request := apitest.MinimalRequest(model.API)
					request.Model = model
					request.Options.Thinking = level
					request.Tools = []llm.ToolDefinition{{Name: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}}
					for round := 0; round < 2; round++ {
						stream, err := service.Stream(t.Context(), request)
						if err != nil {
							t.Fatal(err)
						}
						events := apitest.CollectEvents(t, stream)
						if err := stream.Close(); err != nil {
							t.Fatal(err)
						}
						if len(events) == 0 || events[0].Type != llm.EventTypeStart {
							t.Fatal("missing stream start")
						}
						done := events[len(events)-1]
						if done.Type != llm.EventTypeDone || done.StopReason != llm.StopReasonToolUse || done.Message == nil {
							t.Fatalf("terminal event = %#v", done)
						}
						if done.Message.Provider != aihubmix.ProviderID || done.Message.ModelID != model.ID || done.Message.Usage.TotalTokens != 15 {
							t.Fatalf("message identity/usage = %#v", done.Message)
						}
						request.Messages = append(request.Messages, *done.Message, llm.ToolResultMessage{
							Role: llm.RoleToolResult, ToolCallID: "call-1", ToolName: "read",
							Content: []llm.ContentPart{llm.NewTextContent("fixture result").Part()},
						})
					}
					if calls != 2 {
						t.Fatalf("requests = %d", calls)
					}
				}
			})
		}
	}
	models := aihubmix.Models()
	models[0].ThinkingLevelMap[llm.ThinkingLevelHigh] = nil
	models[0].InputModalities[0] = "other"
	if !aihubmix.DefaultModel().ThinkingLevelMap.Supports(llm.ThinkingLevelHigh) || aihubmix.DefaultModel().InputModalities[0] != llm.InputModalityText {
		t.Fatal("catalog shares mutable state")
	}
}

func assertThinking(t *testing.T, model llm.Model, level llm.ThinkingLevel, body map[string]any) {
	t.Helper()
	effort := string(level)
	if level == llm.ThinkingLevelOff {
		effort = "none"
	}
	switch model.API {
	case openairesponses.API:
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != effort {
			t.Errorf("reasoning = %#v", body["reasoning"])
		}
	case anthropic.API:
		thinking, ok := body["thinking"].(map[string]any)
		kind := "adaptive"
		if level == llm.ThinkingLevelOff {
			kind = "disabled"
		}
		if !ok || thinking["type"] != kind || thinking["budget_tokens"] != nil {
			t.Errorf("thinking = %#v", thinking)
		}
		output, _ := body["output_config"].(map[string]any)
		if level != llm.ThinkingLevelOff && output["effort"] != effort {
			t.Errorf("output_config = %#v", output)
		}
	default:
		if model.ID == "deepseek-v4.1-flash" {
			thinking, _ := body["thinking"].(map[string]any)
			kind := "enabled"
			if level == llm.ThinkingLevelOff {
				kind = "disabled"
			}
			if thinking["type"] != kind {
				t.Errorf("thinking = %#v", thinking)
			}
			if level == llm.ThinkingLevelOff {
				return
			}
		}
		if body["reasoning_effort"] != effort {
			t.Errorf("reasoning_effort = %#v", body["reasoning_effort"])
		}
	}
}

func toolSSE(api llm.API) string {
	events := []string{
		`{"id":"msg-1","choices":[{"index":0,"delta":{"reasoning_content":"plan","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`[DONE]`,
	}
	if api == openairesponses.API {
		events = []string{
			`{"type":"response.created","response":{"id":"resp-1","status":"in_progress","output":[]}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"read","arguments":"{\"path\":\"README.md\"}","status":"completed"}}`,
			`{"type":"response.completed","response":{"id":"resp-1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		}
	}
	if api == anthropic.API {
		events = []string{
			`{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan","signature":"opaque-signature"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-1","name":"read","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}
	}
	var result strings.Builder
	for _, event := range events {
		if api == anthropic.API {
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(event), &envelope)
			result.WriteString("event: " + envelope.Type + "\n")
		}
		result.WriteString("data: " + event + "\n\n")
	}
	return result.String()
}
