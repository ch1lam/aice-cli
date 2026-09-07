package zhipu_test

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/apitest"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/zhipu"
)

func TestCatalogCapabilitiesAndWireControls(t *testing.T) {
	t.Parallel()
	// Expected facts are independent of catalog construction and wire mapping.
	cases := []struct {
		id              string
		context, output int64
		image           bool
		levels          []llm.ThinkingLevel
		effort          bool
	}{
		{"glm-5.3", 1_000_000, 131_072, false, []llm.ThinkingLevel{"low", "high", "max"}, true},
		{"glm-5.3-flash", 1_000_000, 131_072, true, []llm.ThinkingLevel{"low", "high", "max"}, true},
		{"glm-5.2", 1_000_000, 131_072, false, []llm.ThinkingLevel{"off", "high", "max"}, true},
		{"glm-5.1", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-5", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-5-turbo", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.7", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.7-flashx", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.7-flash", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.6", 200_000, 131_072, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.5-air", 128_000, 98_304, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.5-airx", 128_000, 98_304, false, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4-flashx-250414", 128_000, 16_384, false, []llm.ThinkingLevel{"off"}, false},
		{"glm-4-flash-250414", 128_000, 16_384, false, []llm.ThinkingLevel{"off"}, false},
		{"glm-5v-turbo", 200_000, 131_072, true, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.6v", 128_000, 32_768, true, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.6v-flashx", 128_000, 32_768, true, []llm.ThinkingLevel{"off", "high"}, false},
		{"glm-4.6v-flash", 128_000, 32_768, true, []llm.ThinkingLevel{"off", "high"}, false},
	}
	models := zhipu.Models()
	if len(models) != len(cases) {
		t.Fatalf("models = %d, want %d", len(models), len(cases))
	}
	for i, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			model := models[i]
			if model.ID != tc.id || model.ContextWindow != tc.context || model.MaxTokens != tc.output || !slices.Equal(llm.SupportedThinkingLevels(model), tc.levels) {
				t.Fatalf("catalog = %#v", model)
			}
			if slices.Contains(model.InputModalities, llm.InputModalityImage) != tc.image {
				t.Fatal("wrong image capability")
			}
			for _, level := range tc.levels {
				t.Run(string(level), func(t *testing.T) {
					service, err := zhipu.New(zhipu.Config{APIKey: "fake", HTTPClient: &http.Client{Transport: apitest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							return nil, err
						}
						thinking, hasThinking := body["thinking"].(map[string]any)
						if len(tc.levels) == 1 {
							if hasThinking {
								t.Error("non-thinking model received thinking control")
							}
						} else {
							want := "enabled"
							if level == "off" {
								want = "disabled"
							}
							if !hasThinking || thinking["type"] != want {
								t.Errorf("thinking = %#v", body["thinking"])
							}
						}
						wantEffort := ""
						if tc.effort && level != "off" {
							wantEffort = string(level)
						}
						effort, _ := body["reasoning_effort"].(string)
						if effort != wantEffort {
							t.Errorf("effort = %q, want %q", effort, wantEffort)
						}
						if tc.image {
							content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
							image := content[1].(map[string]any)["image_url"].(map[string]any)
							if image["url"] != "data:image/png;base64,aW1hZ2U=" {
								t.Errorf("image = %#v", image)
							}
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
					})}})
					if err != nil {
						t.Fatal(err)
					}
					request := apitest.MinimalRequest(model.API)
					request.Model = model
					request.Options.Thinking = level
					if tc.image {
						request.Messages = []llm.Message{llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("describe").Part(), {Type: llm.ContentTypeImage, Image: &llm.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}}}}
					}
					stream, err := service.Stream(t.Context(), request)
					if err != nil {
						t.Fatal(err)
					}
					if err := stream.Close(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
	coding := zhipu.CodingPlan().Models()
	if len(coding) != 2 || coding[0].ID != "glm-5.3" || coding[1].ID != "glm-5.3-flash" {
		t.Fatalf("coding catalog = %#v", coding)
	}
}

func TestCodingRejectsPlatformOnlyModels(t *testing.T) {
	t.Parallel()
	service, err := zhipu.NewCoding(zhipu.Config{APIKey: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range zhipu.Models()[2:] {
		model.Provider = zhipu.CodingProviderID
		request := apitest.MinimalRequest(model.API)
		request.Model = model
		if _, err := service.Stream(t.Context(), request); err == nil || !strings.Contains(err.Error(), "unsupported model") {
			t.Fatalf("%s error = %v", model.ID, err)
		}
	}
}
