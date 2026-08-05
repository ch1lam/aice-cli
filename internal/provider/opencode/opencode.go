// Package opencode defines OpenCode Go models and compatibility behavior.
//
// OpenCode Go is the OpenAI-compatible model gateway hosted by opencode.ai at
// https://opencode.ai/zen/go/v1. All of its models speak the Chat Completions
// wire protocol, so this provider reuses the openai-completions adapter rather
// than the Anthropic or Responses adapters that DeepSeek selects between.
package opencode

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	// ProviderID is OpenCode Go's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "opencode-go"
	// BaseURL is OpenCode Go's OpenAI-compatible API root. The completions
	// adapter posts to {BaseURL}/chat/completions.
	BaseURL = "https://opencode.ai/zen/go/v1"
)

// Config contains OpenCode Go connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider applies OpenCode Go compatibility rules before delegating to the
// Chat Completions wire protocol.
type Provider struct {
	completionsAdapter *openaicompletions.Adapter
}

// New constructs an OpenCode Go provider. An empty BaseURL selects the official
// gateway endpoint.
func New(config Config) (*Provider, error) {
	baseURL := BaseURL
	if strings.TrimSpace(config.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	}
	completionsAdapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     config.APIKey,
		BaseURL:    baseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure completions adapter: %w", err)
	}
	return &Provider{completionsAdapter: completionsAdapter}, nil
}

// Models returns the OpenCode Go models supported by this provider.
func Models() []llm.Model {
	return catalog
}

// DefaultModel returns the fast model used when no explicit model is selected.
func DefaultModel() llm.Model {
	return model("deepseek-v4-flash")
}

// Stream validates provider-specific compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"opencode-go: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if !knownModel(request.Model.ID) {
		return nil, fmt.Errorf("opencode-go: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != openaicompletions.API {
		return nil, fmt.Errorf(
			"opencode-go: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			openaicompletions.API,
		)
	}
	if err := validateMessages(request.Messages); err != nil {
		return nil, err
	}
	return p.completionsAdapter.Stream(ctx, request)
}

func knownModel(id string) bool {
	for _, candidate := range catalog {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

// catalog mirrors the OpenCode Go model list published by models.dev. Context
// window, output limits, and pricing are USD per million tokens from that
// catalog. Regenerate from https://models.dev/api.json instead of hand-editing.
var catalog = []llm.Model{
	model("deepseek-v4-flash"),
	model("deepseek-v4-pro"),
	model("kimi-k2.5"),
	model("kimi-k2.6"),
	model("kimi-k2.7-code"),
	model("kimi-k3"),
	model("glm-5"),
	model("glm-5.1"),
	model("glm-5.2"),
	model("qwen3.5-plus"),
	model("qwen3.6-plus"),
	model("qwen3.7-plus"),
	model("qwen3.7-max"),
	model("qwen3.8-max"),
	model("minimax-m2.5"),
	model("minimax-m2.7"),
	model("minimax-m3"),
	model("mimo-v2-omni"),
	model("mimo-v2-pro"),
	model("mimo-v2.5"),
	model("mimo-v2.5-pro"),
	model("gpt-5.6-luna"),
	model("grok-4.5"),
	model("hy3"),
}

func model(id string) llm.Model {
	spec := modelSpecs[id]
	return llm.Model{
		ID:               id,
		Name:             spec.name,
		API:              openaicompletions.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		InputModalities:  []llm.InputModality{llm.InputModalityText},
		ContextWindow:    spec.contextWindow,
		MaxTokens:        spec.maxTokens,
		Pricing: llm.Pricing{
			Input:     spec.input,
			Output:    spec.output,
			CacheRead: spec.cacheRead,
		},
	}
}

type modelSpec struct {
	name          string
	contextWindow int64
	maxTokens     int64
	input         float64
	output        float64
	cacheRead     float64
}

var modelSpecs = map[string]modelSpec{
	"deepseek-v4-flash": {name: "DeepSeek V4 Flash", contextWindow: 1_000_000, maxTokens: 384_000, input: 0.14, output: 0.28, cacheRead: 0.0028},
	"deepseek-v4-pro":   {name: "DeepSeek V4 Pro", contextWindow: 1_000_000, maxTokens: 384_000, input: 0.435, output: 0.87, cacheRead: 0.003625},
	"kimi-k2.5":         {name: "Kimi K2.5", contextWindow: 262_144, maxTokens: 65_536, input: 0.6, output: 3, cacheRead: 0.1},
	"kimi-k2.6":         {name: "Kimi K2.6", contextWindow: 262_144, maxTokens: 65_536, input: 0.95, output: 4, cacheRead: 0.16},
	"kimi-k2.7-code":    {name: "Kimi K2.7 Code", contextWindow: 262_144, maxTokens: 262_144, input: 0.95, output: 4, cacheRead: 0.19},
	"kimi-k3":           {name: "Kimi K3", contextWindow: 1_048_576, maxTokens: 131_072, input: 3, output: 15, cacheRead: 0.3},
	"glm-5":             {name: "GLM-5", contextWindow: 202_752, maxTokens: 32_768, input: 1, output: 3.2, cacheRead: 0.2},
	"glm-5.1":           {name: "GLM-5.1", contextWindow: 202_752, maxTokens: 32_768, input: 1.4, output: 4.4, cacheRead: 0.26},
	"glm-5.2":           {name: "GLM-5.2", contextWindow: 1_000_000, maxTokens: 131_072, input: 1.4, output: 4.4, cacheRead: 0.26},
	"qwen3.5-plus":      {name: "Qwen 3.5 Plus", contextWindow: 262_144, maxTokens: 65_536, input: 0.2, output: 1.2, cacheRead: 0.02},
	"qwen3.6-plus":      {name: "Qwen 3.6 Plus", contextWindow: 1_000_000, maxTokens: 65_536, input: 0.5, output: 3, cacheRead: 0.05},
	"qwen3.7-plus":      {name: "Qwen 3.7 Plus", contextWindow: 1_000_000, maxTokens: 65_536, input: 0.4, output: 1.6, cacheRead: 0.04},
	"qwen3.7-max":       {name: "Qwen 3.7 Max", contextWindow: 1_000_000, maxTokens: 65_536, input: 2.5, output: 7.5, cacheRead: 0.5},
	"qwen3.8-max":       {name: "Qwen 3.8 Max", contextWindow: 1_000_000, maxTokens: 131_072, input: 2, output: 6, cacheRead: 0.25},
	"minimax-m2.5":      {name: "MiniMax M2.5", contextWindow: 204_800, maxTokens: 65_536, input: 0.3, output: 1.2, cacheRead: 0.03},
	"minimax-m2.7":      {name: "MiniMax M2.7", contextWindow: 204_800, maxTokens: 131_072, input: 0.3, output: 1.2, cacheRead: 0.06},
	"minimax-m3":        {name: "MiniMax M3", contextWindow: 1_000_000, maxTokens: 131_072, input: 0.3, output: 1.2, cacheRead: 0.06},
	"mimo-v2-omni":      {name: "Mimo V2 Omni", contextWindow: 262_144, maxTokens: 128_000, input: 0.4, output: 2, cacheRead: 0.08},
	"mimo-v2-pro":       {name: "Mimo V2 Pro", contextWindow: 1_048_576, maxTokens: 128_000, input: 1, output: 3, cacheRead: 0.2},
	"mimo-v2.5":         {name: "Mimo V2.5", contextWindow: 1_000_000, maxTokens: 128_000, input: 0.14, output: 0.28, cacheRead: 0.0028},
	"mimo-v2.5-pro":     {name: "Mimo V2.5 Pro", contextWindow: 1_048_576, maxTokens: 128_000, input: 0.435, output: 0.87, cacheRead: 0.003625},
	"gpt-5.6-luna":      {name: "GPT-5.6 Luna", contextWindow: 1_050_000, maxTokens: 128_000, input: 0.1, output: 0.6, cacheRead: 0.01},
	"grok-4.5":          {name: "Grok 4.5", contextWindow: 500_000, maxTokens: 500_000, input: 2, output: 6, cacheRead: 0.5},
	"hy3":               {name: "Hy3", contextWindow: 256_000, maxTokens: 64_000, input: 0.14, output: 0.58, cacheRead: 0.035},
}

func validateMessages(messages []llm.Message) error {
	for messageIndex, message := range messages {
		var content []llm.ContentPart
		switch value := message.(type) {
		case llm.UserMessage:
			content = value.Content
		case llm.AssistantMessage:
			content = value.Content
		case llm.ToolResultMessage:
			for partIndex, part := range value.Content {
				if part.Type != llm.ContentTypeText {
					return fmt.Errorf(
						"opencode-go: message %d content %d: non-text tool results "+
							"are not supported by OpenCode Go models",
						messageIndex,
						partIndex,
					)
				}
			}
			continue
		case nil:
			return fmt.Errorf("opencode-go: message %d is nil", messageIndex)
		default:
			return fmt.Errorf(
				"opencode-go: message %d has unsupported type %T",
				messageIndex,
				message,
			)
		}
		for partIndex, part := range content {
			if part.Type == llm.ContentTypeImage {
				return fmt.Errorf(
					"opencode-go: message %d content %d: image content is not "+
						"supported by OpenCode Go models",
					messageIndex,
					partIndex,
				)
			}
		}
	}
	return nil
}
