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
	anthropicAdapter   *anthropic.Adapter
	completionsAdapter *openaicompletions.Adapter
	responsesAdapter   *openairesponses.Adapter
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
	completionsAdapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     config.APIKey,
		BaseURL:    baseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure completions adapter: %w", err)
	}
	responsesAdapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     config.APIKey,
		BaseURL:    baseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure Responses adapter: %w", err)
	}
	anthropicAdapter, err := anthropic.New(anthropic.Config{
		APIKey:     config.APIKey,
		BaseURL:    anthropicBaseURL(baseURL),
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode-go: configure Anthropic adapter: %w", err)
	}
	return &Provider{
		anthropicAdapter:   anthropicAdapter,
		completionsAdapter: completionsAdapter,
		responsesAdapter:   responsesAdapter,
	}, nil
}

// Models returns the OpenCode Go models supported by this provider.
func Models() []llm.Model {
	models := make([]llm.Model, 0, len(modelIDs))
	for _, id := range modelIDs {
		models = append(models, model(id))
	}
	return models
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
	expectedModel := model(request.Model.ID)
	if request.Model.API != expectedModel.API {
		return nil, fmt.Errorf(
			"opencode-go: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			expectedModel.API,
		)
	}
	capabilities := messageCapabilities
	capabilities.SupportsImage = supportsImage(request.Model.ID)
	if err := provider.ValidateMessages(request.Messages, capabilities); err != nil {
		return nil, err
	}
	switch request.Model.API {
	case anthropic.API:
		return p.anthropicAdapter.Stream(ctx, request)
	case openaicompletions.API:
		return p.completionsAdapter.Stream(ctx, request)
	case openairesponses.API:
		return p.responsesAdapter.Stream(ctx, request)
	default:
		return nil, fmt.Errorf(
			"opencode-go: model %q uses unsupported API %q",
			request.Model.ID,
			request.Model.API,
		)
	}
}

func anthropicBaseURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/v1")
}

func knownModel(id string) bool {
	_, exists := modelSpecs[id]
	return exists
}

// modelSpecs is the active OpenCode Go catalog. Deprecated models remain
// absent so the selector matches the upstream catalog.
var modelSpecs = modelSpecCatalog()

func modelSpecCatalog() map[string]provider.ModelSpec {
	specs := make(map[string]provider.ModelSpec, 26)
	for _, shared := range provider.DeepSeekModelSpecs() {
		specs[shared.ID] = shared
	}
	flash := specs["deepseek-v4-flash"]
	flash.Input = 0.22
	flash.Output = 0.66
	flash.CacheRead = 0.007
	flash.ThinkingLevelMap = llm.ThinkingLevelsMap(
		llm.ThinkingLevelLow,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelMax,
	)
	flash.ThinkingFormat = llm.ThinkingFormatDeepSeek
	flash.SupportsReasoningEffort = true
	specs[flash.ID] = flash

	pro := specs["deepseek-v4-pro"]
	pro.Input = 0.66
	pro.Output = 1.98
	pro.CacheRead = 0.022
	pro.ThinkingLevelMap = llm.ThinkingLevelsMap(
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelMax,
	)
	pro.ThinkingFormat = llm.ThinkingFormatDeepSeek
	pro.SupportsReasoningEffort = true
	specs[pro.ID] = pro

	specs["deepseek-v4-flash-vision-exp"] = provider.ModelSpec{
		ID:            "deepseek-v4-flash-vision-exp",
		Name:          "DeepSeek V4 Flash Vision Exp",
		ContextWindow: 1_000_000,
		MaxTokens:     384_000,
		Input:         0.22,
		Output:        0.66,
		CacheRead:     0.007,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelOff,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		),
		ThinkingFormat:          llm.ThinkingFormatDeepSeek,
		SupportsReasoningEffort: true,
	}

	specs["kimi-k2.6"] = provider.ModelSpec{
		ID: "kimi-k2.6", Name: "Kimi K2.6", ContextWindow: 262_144, MaxTokens: 65_536, Input: 0.95, Output: 4, CacheRead: 0.16,
		// OpenCode Go exposes Kimi K2.6 thinking as a DeepSeek-style toggle
		// with only on/off plus high; the gateway rejects reasoning_effort
		// (Pi's opencode-go override).
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh),
		ThinkingFormat:   llm.ThinkingFormatDeepSeek,
	}
	specs["kimi-k2.7-code"] = standardSpec("kimi-k2.7-code", "Kimi K2.7 Code", 262_144, 262_144, 0.95, 4, 0.19)
	// Kimi K3 always reasons at max.
	specs["kimi-k3"] = provider.ModelSpec{
		ID:               "kimi-k3",
		Name:             "Kimi K3",
		ContextWindow:    1_048_576,
		MaxTokens:        131_072,
		Input:            3,
		Output:           15,
		CacheRead:        0.3,
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelMax),
	}
	specs["glm-5.1"] = standardSpec("glm-5.1", "GLM-5.1", 202_752, 32_768, 1.4, 4.4, 0.26)
	specs["glm-5.2"] = provider.ModelSpec{
		ID: "glm-5.2", Name: "GLM-5.2", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 1.4, Output: 4.4, CacheRead: 0.26,
		// GLM-5.2 always reasons at high or max; the gateway rejects disabling
		// thinking and the lower effort tiers (Pi's opencode-go override).
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
	}
	specs["glm-5.3"] = provider.ModelSpec{
		ID: "glm-5.3", Name: "GLM-5.3", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 1.4, Output: 4.4, CacheRead: 0.26,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		),
	}
	specs["glm-5.3-flash"] = provider.ModelSpec{
		ID: "glm-5.3-flash", Name: "GLM-5.3-Flash", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 0.075, Output: 0.25, CacheRead: 0.015,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelLow,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		),
	}
	specs["qwen3.6-plus"] = standardSpec("qwen3.6-plus", "Qwen3.6 Plus", 1_000_000, 65_536, 0.5, 3, 0.05)
	specs["qwen3.7-plus"] = standardSpec("qwen3.7-plus", "Qwen3.7 Plus", 1_000_000, 65_536, 0.4, 1.6, 0.04)
	specs["qwen3.7-max"] = standardSpec("qwen3.7-max", "Qwen3.7 Max", 1_000_000, 65_536, 2.5, 7.5, 0.5)
	specs["qwen3.8-max"] = standardSpec("qwen3.8-max", "Qwen3.8 Max", 1_000_000, 131_072, 2, 6, 0.25)
	specs["qwen3.8-flash"] = standardSpec("qwen3.8-flash", "Qwen3.8 Flash", 1_000_000, 131_072, 0.15, 0.47, 0.016)
	specs["minimax-m2.7"] = standardSpec("minimax-m2.7", "MiniMax M2.7", 204_800, 131_072, 0.3, 1.2, 0.06)
	specs["minimax-m3"] = standardSpec("minimax-m3", "MiniMax M3", 1_000_000, 131_072, 0.3, 1.2, 0.06)
	specs["mimo-v2.5"] = standardSpec("mimo-v2.5", "MiMo V2.5", 1_000_000, 128_000, 0.14, 0.28, 0.0028)
	specs["mimo-v2.5-pro"] = standardSpec("mimo-v2.5-pro", "MiMo V2.5 Pro", 1_048_576, 128_000, 0.435, 0.87, 0.003625)
	specs["longcat-2.0"] = standardSpec("longcat-2.0", "LongCat-2.0", 1_000_000, 131_072, 0.3, 1.2, 0.006)
	// OpenCode Go exposes off as none plus low through max.
	specs["gpt-5.6-luna"] = provider.ModelSpec{
		ID:            "gpt-5.6-luna",
		Name:          "GPT-5.6 Luna",
		ContextWindow: 1_050_000,
		MaxTokens:     128_000,
		Input:         0.2,
		Output:        1.2,
		CacheRead:     0.02,
		ThinkingLevelMap: llm.ThinkingLevelMap{
			llm.ThinkingLevelOff:     llm.ThinkingValue("none"),
			llm.ThinkingLevelMinimal: nil,
			llm.ThinkingLevelLow:     llm.ThinkingValue("low"),
			llm.ThinkingLevelMedium:  llm.ThinkingValue("medium"),
			llm.ThinkingLevelHigh:    llm.ThinkingValue("high"),
			llm.ThinkingLevelXHigh:   llm.ThinkingValue("xhigh"),
			llm.ThinkingLevelMax:     llm.ThinkingValue("max"),
		},
	}
	// Grok 4.6 exposes low, medium, high, and xhigh efforts.
	specs["grok-4.6"] = provider.ModelSpec{
		ID:            "grok-4.6",
		Name:          "Grok 4.6",
		ContextWindow: 500_000,
		MaxTokens:     500_000,
		Input:         2,
		Output:        6,
		CacheRead:     0.5,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		),
	}
	specs["muse-spark-1.2-contributor"] = provider.ModelSpec{
		ID:            "muse-spark-1.2-contributor",
		Name:          "Muse Spark 1.2 Contributor",
		ContextWindow: 1_048_576,
		MaxTokens:     131_072,
		Input:         0.1,
		Output:        0.2,
		CacheRead:     0.002,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelMinimal,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		),
	}
	specs["muse-spark-1.3-contributor"] = provider.ModelSpec{
		ID:            "muse-spark-1.3-contributor",
		Name:          "Muse Spark 1.3 Contributor",
		ContextWindow: 1_048_576,
		MaxTokens:     131_072,
		Input:         0.1,
		Output:        0.2,
		CacheRead:     0.002,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelMinimal,
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
		),
	}
	// Hy3 maps off to none and exposes low and high.
	specs["hy3"] = provider.ModelSpec{
		ID:            "hy3",
		Name:          "Hy3",
		ContextWindow: 256_000,
		MaxTokens:     128_000,
		Input:         0.14,
		Output:        0.58,
		CacheRead:     0.035,
		ThinkingLevelMap: llm.ThinkingLevelMap{
			llm.ThinkingLevelOff:     llm.ThinkingValue("none"),
			llm.ThinkingLevelMinimal: nil,
			llm.ThinkingLevelLow:     llm.ThinkingValue("low"),
			llm.ThinkingLevelMedium:  nil,
			llm.ThinkingLevelHigh:    llm.ThinkingValue("high"),
			llm.ThinkingLevelXHigh:   nil,
			llm.ThinkingLevelMax:     nil,
		},
	}
	// Hy4 preview maps off to none and exposes high.
	specs["hy4-preview"] = provider.ModelSpec{
		ID:            "hy4-preview",
		Name:          "Hy4 preview",
		ContextWindow: 1_024_000,
		MaxTokens:     64_000,
		Input:         0.834,
		Output:        2.501,
		CacheRead:     0.042,
		ThinkingLevelMap: llm.ThinkingLevelMap{
			llm.ThinkingLevelOff:     llm.ThinkingValue("none"),
			llm.ThinkingLevelMinimal: nil,
			llm.ThinkingLevelLow:     nil,
			llm.ThinkingLevelMedium:  nil,
			llm.ThinkingLevelHigh:    llm.ThinkingValue("high"),
			llm.ThinkingLevelXHigh:   nil,
			llm.ThinkingLevelMax:     nil,
		},
	}
	return specs
}

// standardSpec builds a spec with the standard five-level capability map
// (off through high, canonical wire tokens).
func standardSpec(
	id, name string,
	contextWindow, maxTokens int64,
	input, output, cacheRead float64,
) provider.ModelSpec {
	return provider.ModelSpec{
		ID: id, Name: name, ContextWindow: contextWindow, MaxTokens: maxTokens,
		Input: input, Output: output, CacheRead: cacheRead,
		ThinkingLevelMap: llm.StandardThinkingLevelMap(),
	}
}

// modelIDs lists the OpenCode Go models in menu order; Models constructs
// fresh values so callers cannot mutate catalog maps shared by later runs.
var modelIDs = []string{
	"grok-4.6",
	"gpt-5.6-luna",
	"glm-5.3-flash",
	"glm-5.3",
	"glm-5.2",
	"glm-5.1",
	"kimi-k3",
	"kimi-k2.7-code",
	"kimi-k2.6",
	"longcat-2.0",
	"deepseek-v4-pro",
	"deepseek-v4-flash",
	"deepseek-v4-flash-vision-exp",
	"mimo-v2.5",
	"mimo-v2.5-pro",
	"minimax-m3",
	"minimax-m2.7",
	"muse-spark-1.3-contributor",
	"muse-spark-1.2-contributor",
	"qwen3.8-max",
	"qwen3.8-flash",
	"qwen3.7-max",
	"qwen3.7-plus",
	"qwen3.6-plus",
	"hy4-preview",
	"hy3",
}

func model(id string) llm.Model {
	spec := modelSpecs[id]
	return llm.Model{
		ID:                      id,
		Name:                    spec.Name,
		API:                     modelAPI(id),
		Provider:                ProviderID,
		SupportsThinking:        true,
		ThinkingLevelMap:        spec.ThinkingLevelMap.Clone(),
		ThinkingFormat:          spec.ThinkingFormat,
		SupportsReasoningEffort: spec.SupportsReasoningEffort,
		OmitMaxTokensByDefault:  true,
		InputModalities:         inputModalities(id),
		ContextWindow:           spec.ContextWindow,
		MaxTokens:               spec.MaxTokens,
		Pricing: llm.Pricing{
			Input:      spec.Input,
			Output:     spec.Output,
			CacheRead:  spec.CacheRead,
			CacheWrite: cacheWritePrice(id),
		},
	}
}

func modelAPI(id string) llm.API {
	switch id {
	case "gpt-5.6-luna", "grok-4.6", "muse-spark-1.2-contributor", "muse-spark-1.3-contributor":
		return openairesponses.API
	case "minimax-m2.7",
		"minimax-m3",
		"qwen3.6-plus",
		"qwen3.7-max",
		"qwen3.7-plus",
		"qwen3.8-max",
		"qwen3.8-flash":
		return anthropic.API
	default:
		return openaicompletions.API
	}
}

func inputModalities(id string) []llm.InputModality {
	modalities := []llm.InputModality{llm.InputModalityText}
	if supportsImage(id) {
		modalities = append(modalities, llm.InputModalityImage)
	}
	return modalities
}

func supportsImage(id string) bool {
	switch id {
	case "deepseek-v4-flash-vision-exp",
		"glm-5.3-flash",
		"gpt-5.6-luna",
		"grok-4.6",
		"kimi-k2.6",
		"kimi-k2.7-code",
		"kimi-k3",
		"minimax-m3",
		"mimo-v2.5",
		"muse-spark-1.2-contributor",
		"muse-spark-1.3-contributor",
		"qwen3.6-plus",
		"qwen3.7-plus",
		"qwen3.8-max",
		"qwen3.8-flash":
		return true
	default:
		return false
	}
}

func cacheWritePrice(id string) float64 {
	switch id {
	case "gpt-5.6-luna":
		return 0.25
	case "minimax-m2.7":
		return 0.375
	case "qwen3.6-plus":
		return 0.625
	case "qwen3.7-plus":
		return 0.5
	case "qwen3.7-max":
		return 3.125
	case "qwen3.8-max":
		return 2.5
	case "qwen3.8-flash":
		return 0.2
	default:
		return 0
	}
}

// messageCapabilities declares shared OpenCode Go content support. Image
// support is enabled per model immediately before validation.
var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "OpenCode Go",
	SupportsImage:            false,
	SupportsRedactedThinking: true,
	NestedToolResultTextOnly: false,
}

// Label returns the OpenCode Go display name.
func (p *Provider) Label() string {
	return "OpenCode Go"
}

// MenuDescription describes OpenCode Go in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return fmt.Sprintf(
		"OpenCode Go subscription (%d models via OpenAI-compatible APIs)",
		len(modelIDs),
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
