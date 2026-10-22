// Package opencode defines OpenCode Go models and compatibility behavior.
//
// OpenCode Go is the OpenAI-compatible model gateway hosted by opencode.ai at
// https://opencode.ai/zen/go/v1. Its catalog spans Chat Completions, Anthropic
// Messages, and OpenAI Responses wire protocols.
package opencode

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	// ProviderID is OpenCode Go's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "opencode-go"
	// BaseURL is OpenCode Go's OpenAI-compatible API root.
	BaseURL = "https://opencode.ai/zen/go/v1"
)

// Config contains OpenCode Go connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider applies OpenCode Go compatibility rules before delegating to the
// wire protocol selected by the request model.
type Provider struct {
	route provider.Route
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs an OpenCode Go provider. An empty BaseURL selects the official
// gateway endpoint.
func New(config Config) (*Provider, error) {
	baseURL := BaseURL
	if strings.TrimSpace(config.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	}
	client := routingClient(config.HTTPClient)
	completionsAdapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     config.APIKey,
		BaseURL:    baseURL,
		HTTPClient: client,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure completions adapter: %w", err)
	}
	responsesAdapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     config.APIKey,
		BaseURL:    baseURL,
		HTTPClient: client,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure Responses adapter: %w", err)
	}
	anthropicAdapter, err := anthropic.New(anthropic.Config{
		APIKey:     config.APIKey,
		BaseURL:    anthropicBaseURL(baseURL),
		HTTPClient: client,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure Anthropic adapter: %w", err)
	}
	return &Provider{route: provider.Route{
		Provider: ProviderID,
		Label:    "OpenCode Go",
		Models:   Models(),
		Adapters: map[llm.API]llm.Streamer{
			anthropic.API:         anthropicAdapter,
			openaicompletions.API: completionsAdapter,
			openairesponses.API:   responsesAdapter,
		},
		RedactedThinking: []llm.API{anthropic.API, openaicompletions.API, openairesponses.API},
	}}, nil
}

// Stream validates provider-specific compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
}

func anthropicBaseURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/v1")
}

// Models returns the OpenCode Go catalog in menu order. It follows the
// gateway's /zen/go/v1/models endpoint (checked 2026-10-21); models still
// served but omitted from the Go documentation list are retained for existing
// selections. Gemini 4 Argon Contributor is advertised by OpenCode Go but
// served only over Google's generative API, which AICE does not implement.
// Each call builds fresh values so callers cannot mutate a shared catalog.
func Models() []llm.Model {
	models := []llm.Model{
		{
			ID: "grok-4.7", Name: "Grok 4.7", API: openairesponses.API,
			ContextWindow: 500_000, MaxTokens: 500_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh),
			Pricing:          llm.Pricing{Input: 2, Output: 6, CacheRead: 0.5},
		},
		{
			ID: "grok-4.6", Name: "Grok 4.6", API: openairesponses.API,
			ContextWindow: 500_000, MaxTokens: 500_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh),
			Pricing:          llm.Pricing{Input: 2, Output: 6, CacheRead: 0.5},
		},
		// GPT-6 Luna and GPT-5.6 Luna expose off as none plus low through max.
		{
			ID: "gpt-6-luna", Name: "GPT-6 Luna", API: openairesponses.API,
			ContextWindow: 1_050_000, MaxTokens: 128_000, InputModalities: imageInput(),
			ThinkingLevelMap: noneOff(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax),
			Pricing:          llm.Pricing{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125},
		},
		{
			ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", API: openairesponses.API,
			ContextWindow: 1_050_000, MaxTokens: 128_000, InputModalities: imageInput(),
			ThinkingLevelMap: noneOff(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax),
			Pricing:          llm.Pricing{Input: 0.2, Output: 1.2, CacheRead: 0.02, CacheWrite: 0.25},
		},
		// OpenCode Go exposes Claude Haiku 5.5 adaptive thinking at xhigh and max.
		{
			ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 128_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelXHigh, llm.ThinkingLevelMax),
			ThinkingFormat:   llm.ThinkingFormatAdaptive, SupportsReasoningEffort: true,
			Pricing: llm.Pricing{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125},
		},
		{
			ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			Pricing:          llm.Pricing{Input: 0.15, Output: 0.5, CacheRead: 0.03},
		},
		{
			ID: "glm-5.3", Name: "GLM-5.3", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: textInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			Pricing:          llm.Pricing{Input: 1.4, Output: 4.4, CacheRead: 0.26},
		},
		// GLM-5.2 always reasons at high or max; the gateway rejects disabling
		// thinking and the lower effort tiers (Pi's opencode-go override).
		{
			ID: "glm-5.2", Name: "GLM-5.2", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: textInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			Pricing:          llm.Pricing{Input: 1.4, Output: 4.4, CacheRead: 0.26},
		},
		{
			ID: "glm-5.1", Name: "GLM-5.1", API: openaicompletions.API,
			ContextWindow: 202_752, MaxTokens: 32_768, InputModalities: textInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 1.4, Output: 4.4, CacheRead: 0.26},
		},
		// Kimi K3 always reasons at max.
		{
			ID: "kimi-k3", Name: "Kimi K3", API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelMax),
			Pricing:          llm.Pricing{Input: 3, Output: 15, CacheRead: 0.3},
		},
		{
			ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", API: openaicompletions.API,
			ContextWindow: 262_144, MaxTokens: 262_144, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.95, Output: 4, CacheRead: 0.19},
		},
		// OpenCode Go exposes Kimi K2.6 thinking as a DeepSeek-style toggle
		// with only on/off plus high; the gateway rejects reasoning_effort
		// (Pi's opencode-go override).
		{
			ID: "kimi-k2.6", Name: "Kimi K2.6", API: openaicompletions.API,
			ContextWindow: 262_144, MaxTokens: 65_536, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh),
			ThinkingFormat:   llm.ThinkingFormatDeepSeek,
			Pricing:          llm.Pricing{Input: 0.95, Output: 4, CacheRead: 0.16},
		},
		{
			ID: "longcat-2.5-preview-free", Name: "LongCat 2.5 Preview Free", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
		},
		{
			ID: "longcat-2.0", Name: "LongCat-2.0", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: textInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.3, Output: 1.2, CacheRead: 0.006},
		},
		{
			ID: "step-5-preview", Name: "Step 5 Preview", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 65_536, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh),
			Pricing:          llm.Pricing{Input: 1, Output: 2.7, CacheRead: 0.1},
		},
		{
			ID: "beam-free", Name: "Beam Free", API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072, InputModalities: textInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax),
		},
		// Omen Alpha is served by the gateway endpoint but is absent from the Go
		// documentation list and models.dev; retain its historical metadata for
		// existing selections.
		{
			ID: "omen-alpha", Name: "Omen Alpha", API: openaicompletions.API,
			ContextWindow: 500_000, MaxTokens: 128_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh),
			Pricing:          llm.Pricing{Input: 0.2, Output: 0.66, CacheRead: 0.04},
		},
		{
			ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 384_000, InputModalities: textInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			ThinkingFormat:   llm.ThinkingFormatDeepSeek, SupportsReasoningEffort: true,
			Pricing: llm.Pricing{Input: 0.66, Output: 1.98, CacheRead: 0.022},
		},
		// OpenCode Go retired the V4 Flash ID and serves V4.1 Flash instead.
		{
			ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 384_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			ThinkingFormat:   llm.ThinkingFormatDeepSeek, SupportsReasoningEffort: true,
			Pricing: llm.Pricing{Input: 0.15, Output: 0.6, CacheRead: 0.003},
		},
		{
			ID: "mimo-v2.6-pro", Name: "MiMo V2.6 Pro", API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.435, Output: 0.87, CacheRead: 0.003625},
		},
		{
			ID: "mimo-v2.6-flash", Name: "MiMo V2.6 Flash", API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.14, Output: 0.28, CacheRead: 0.0028},
		},
		{
			ID: "mimo-v2.5", Name: "MiMo V2.5", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 128_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.14, Output: 0.28, CacheRead: 0.0028},
		},
		{
			ID: "mimo-v2.5-pro", Name: "MiMo V2.5 Pro", API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 128_000, InputModalities: textInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.435, Output: 0.87, CacheRead: 0.003625},
		},
		{
			ID: "minimax-m3", Name: "MiniMax M3", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.3, Output: 1.2, CacheRead: 0.06},
		},
		{
			ID: "minimax-m3.1-flash-free", Name: "MiniMax M3.1 Flash Free", API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 512_000, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax),
		},
		{
			ID: "minimax-m2.7", Name: "MiniMax M2.7", API: anthropic.API,
			ContextWindow: 204_800, MaxTokens: 131_072, InputModalities: textInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0.375},
		},
		{
			ID: "minimax-m2.5", Name: "MiniMax M2.5", API: anthropic.API,
			ContextWindow: 204_800, MaxTokens: 65_536, InputModalities: textInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.3, Output: 1.2, CacheRead: 0.06, CacheWrite: 0.375},
		},
		{
			ID: "muse-spark-1.3-contributor", Name: "Muse Spark 1.3 Contributor", API: openairesponses.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelMinimal, llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh),
			Pricing:          llm.Pricing{Input: 0.1, Output: 0.2, CacheRead: 0.002},
		},
		{
			ID: "muse-spark-1.2-contributor", Name: "Muse Spark 1.2 Contributor", API: openairesponses.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelMinimal, llm.ThinkingLevelLow, llm.ThinkingLevelMedium, llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh),
			Pricing:          llm.Pricing{Input: 0.1, Output: 0.2, CacheRead: 0.002},
		},
		{
			ID: "qwen3.8-max", Name: "Qwen3.8 Max", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 2, Output: 6, CacheRead: 0.25, CacheWrite: 2.5},
		},
		{
			ID: "qwen3.8-flash", Name: "Qwen3.8 Flash", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 131_072, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.15, Output: 0.47, CacheRead: 0.016, CacheWrite: 0.2},
		},
		{
			ID: "qwen3.7-max", Name: "Qwen3.7 Max", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 65_536, InputModalities: textInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 2.5, Output: 7.5, CacheRead: 0.5, CacheWrite: 3.125},
		},
		{
			ID: "qwen3.7-plus", Name: "Qwen3.7 Plus", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 65_536, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.4, Output: 1.6, CacheRead: 0.04, CacheWrite: 0.5},
		},
		{
			ID: "qwen3.6-plus", Name: "Qwen3.6 Plus", API: anthropic.API,
			ContextWindow: 1_000_000, MaxTokens: 65_536, InputModalities: imageInput(),
			ThinkingLevelMap: llm.StandardThinkingLevelMap(),
			Pricing:          llm.Pricing{Input: 0.5, Output: 3, CacheRead: 0.05, CacheWrite: 0.625},
		},
		{
			ID: "hy4-preview", Name: "Hy4 preview", API: openaicompletions.API,
			ContextWindow: 1_024_000, MaxTokens: 64_000, InputModalities: textInput(),
			ThinkingLevelMap: noneOff(llm.ThinkingLevelHigh),
			Pricing:          llm.Pricing{Input: 0.834, Output: 2.501, CacheRead: 0.042},
		},
		{
			ID: "hy3", Name: "Hy3", API: openaicompletions.API,
			ContextWindow: 256_000, MaxTokens: 128_000, InputModalities: textInput(),
			ThinkingLevelMap: noneOff(llm.ThinkingLevelLow, llm.ThinkingLevelHigh),
			Pricing:          llm.Pricing{Input: 0.14, Output: 0.58, CacheRead: 0.035},
		},
	}
	for index := range models {
		models[index].Provider = ProviderID
		models[index].SupportsThinking = true
		models[index].OmitMaxTokensByDefault = true
	}
	return models
}

// DefaultModel returns the fast model used when no explicit model is selected.
func DefaultModel() llm.Model {
	for _, model := range Models() {
		if model.ID == defaultModelID {
			return model
		}
	}
	panic("opencode: default model is missing from the catalog")
}

const defaultModelID = "deepseek-v4.1-flash"

func textInput() []llm.InputModality { return []llm.InputModality{llm.InputModalityText} }

func imageInput() []llm.InputModality {
	return []llm.InputModality{llm.InputModalityText, llm.InputModalityImage}
}

// noneOff builds a thinking map for levels whose off setting the gateway
// names none.
func noneOff(levels ...llm.ThinkingLevel) llm.ThinkingLevelMap {
	result := llm.ThinkingLevelsMap(levels...)
	result[llm.ThinkingLevelOff] = llm.ThinkingValue("none")
	return result
}

// Label returns the OpenCode Go display name.
func (p *Provider) Label() string {
	return "OpenCode Go"
}

// MenuDescription describes OpenCode Go in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return fmt.Sprintf(
		"OpenCode Go subscription (%d models via OpenAI-compatible APIs)",
		len(Models()),
	)
}

// Models returns the OpenCode Go model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the OpenCode Go model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// Configured reports whether the configuration carries an OpenCode Go
// credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.OpenCodeAPIKey != ""
}

// New constructs the credentialed OpenCode Go model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.OpenCodeAPIKey,
		BaseURL: configuration.OpenCodeBaseURL,
	})
}

// SaveAPIKey stores the OpenCode Go credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveOpenCodeAPIKey(apiKey)
}

// ApplyAPIKey stores the OpenCode Go credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.OpenCodeAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing OpenCode Go credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"OpenCode Go API key is not configured; run /login in interactive mode or set %s",
		config.EnvOpenCodeAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
