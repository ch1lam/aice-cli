package moonshot_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/moonshot"
)

func TestCatalogResponsesAndThinking(t *testing.T) {
	t.Parallel()
	for _, model := range moonshot.Models() {
		for _, level := range llm.SupportedThinkingLevels(model) {
			t.Run(model.ID+"/"+string(level), func(t *testing.T) {
				calls := 0
				service, err := moonshot.New(moonshot.Config{
					APIKey: "platform-key",
					HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.URL.String() != "https://api.moonshot.cn/v1/responses" {
							t.Errorf("default endpoint = %s", r.URL)
						}
						if r.Header.Get("Authorization") != "Bearer platform-key" {
							t.Error("wrong credential")
						}
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body["model"] != model.ID || body["stream"] != true {
							t.Errorf("body = %#v", body)
						}
						wantEffort := string(level)
						if level == llm.ThinkingLevelOff {
							wantEffort = "none"
						}
						reasoning, ok := body["reasoning"].(map[string]any)
						if !ok || reasoning["effort"] != wantEffort {
							t.Errorf("reasoning = %#v, want effort %q", body["reasoning"], wantEffort)
						}
						wantContext, wantOutput := int64(262144), float64(32768)
						if model.ID == "kimi-k3" {
							wantContext, wantOutput = 1048576, 131072
						}
						if model.ContextWindow != wantContext || body["max_output_tokens"] != wantOutput {
							t.Error("wrong Responses budgets")
						}
						if _, ok := body["thinking"]; ok {
							t.Error("unexpected Chat Completions thinking toggle")
						}
						if _, ok := body["reasoning_effort"]; ok {
							t.Error("unexpected Chat Completions effort")
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
					})},
				})
				if err != nil {
					t.Fatal(err)
				}
				request := apitest.MinimalRequest(model.API)
				request.Model = model
				request.Options.Thinking = level
				stream, err := service.Stream(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("requests = %d", calls)
				}
			})
		}
	}
	descriptor := &moonshot.Provider{}
	if descriptor.Configured(config.Config{KimiAPIKey: "coding-key", OpenAIAPIKey: "openai-key"}) {
		t.Error("accepted another provider's key")
	}
}
