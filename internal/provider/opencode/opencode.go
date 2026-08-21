// Package opencode defines OpenCode Go models and compatibility behavior.
//
// OpenCode Go is the OpenAI-compatible model gateway hosted by opencode.ai at
// https://opencode.ai/zen/go/v1. All of its models speak the Chat Completions
// wire protocol, so this provider reuses the openai-completions adapter.
package opencode

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
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
	return &Provider{completionsAdapter: completionsAdapter}, nil
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
	if request.Model.API != openaicompletions.API {
		return nil, fmt.Errorf(
			"opencode-go: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			openaicompletions.API,
		)
	}
	if err := provider.ValidateMessages(request.Messages, messageCapabilities); err != nil {
		return nil, err
	}
	return p.completionsAdapter.Stream(ctx, request)
}

func knownModel(id string) bool {
	_, exists := modelSpecs[id]
	return exists
}

// modelSpecs is the OpenCode Go catalog; the DeepSeek models share the specs
// declared in the provider package.
var modelSpecs = modelSpecCatalog()

func modelSpecCatalog() map[string]provider.ModelSpec {
	specs := make(map[string]provider.ModelSpec, 24)
	for _, shared := range provider.DeepSeekModelSpecs() {
		specs[shared.ID] = shared
	}
	// OpenCode Go serves the DeepSeek models through the documented OpenAI
	// shape: thinking toggles the mode and reasoning_effort selects low, high,
	// or max. The shared specs own the available choices.
	for _, id := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
		spec := specs[id]
		spec.ThinkingFormat = llm.ThinkingFormatDeepSeek
		spec.SupportsReasoningEffort = true
		specs[id] = spec
	}

	specs["kimi-k2.5"] = standardSpec("kimi-k2.5", "Kimi K2.5", 262_144, 65_536, 0.6, 3, 0.1)
	specs["kimi-k2.6"] = provider.ModelSpec{
		ID: "kimi-k2.6", Name: "Kimi K2.6", ContextWindow: 262_144, MaxTokens: 65_536, Input: 0.95, Output: 4, CacheRead: 0.16,
		// OpenCode Go exposes Kimi K2.6 thinking as a DeepSeek-style toggle
		// with only on/off plus high; the gateway rejects reasoning_effort
		// (Pi's opencode-go override).
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh),
		ThinkingFormat:   llm.ThinkingFormatDeepSeek,
	}
	specs["kimi-k2.7-code"] = standardSpec("kimi-k2.7-code", "Kimi K2.7 Code", 262_144, 262_144, 0.95, 4, 0.19)
	// Kimi K3 always reasons at max (Pi 0.84.1 opencode-go catalog).
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
	specs["glm-5"] = standardSpec("glm-5", "GLM-5", 202_752, 32_768, 1, 3.2, 0.2)
	specs["glm-5.1"] = standardSpec("glm-5.1", "GLM-5.1", 202_752, 32_768, 1.4, 4.4, 0.26)
	specs["glm-5.2"] = provider.ModelSpec{
		ID: "glm-5.2", Name: "GLM-5.2", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 1.4, Output: 4.4, CacheRead: 0.26,
		// GLM-5.2 always reasons at high or max; the gateway rejects disabling
		// thinking and the lower effort tiers (Pi's opencode-go override).
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
	}
	specs["qwen3.5-plus"] = qwenSpec("qwen3.5-plus", "Qwen 3.5 Plus", 262_144, 65_536, 0.2, 1.2, 0.02)
	specs["qwen3.6-plus"] = qwenSpec("qwen3.6-plus", "Qwen 3.6 Plus", 1_000_000, 65_536, 0.5, 3, 0.05)
	specs["qwen3.7-plus"] = standardSpec("qwen3.7-plus", "Qwen 3.7 Plus", 1_000_000, 65_536, 0.4, 1.6, 0.04)
	specs["qwen3.7-max"] = standardSpec("qwen3.7-max", "Qwen 3.7 Max", 1_000_000, 65_536, 2.5, 7.5, 0.5)
	specs["qwen3.8-max"] = standardSpec("qwen3.8-max", "Qwen 3.8 Max", 1_000_000, 131_072, 2, 6, 0.25)
	specs["minimax-m2.5"] = standardSpec("minimax-m2.5", "MiniMax M2.5", 204_800, 65_536, 0.3, 1.2, 0.03)
	specs["minimax-m2.7"] = standardSpec("minimax-m2.7", "MiniMax M2.7", 204_800, 131_072, 0.3, 1.2, 0.06)
	specs["minimax-m3"] = standardSpec("minimax-m3", "MiniMax M3", 1_000_000, 131_072, 0.3, 1.2, 0.06)
	specs["mimo-v2-omni"] = standardSpec("mimo-v2-omni", "Mimo V2 Omni", 262_144, 128_000, 0.4, 2, 0.08)
	specs["mimo-v2-pro"] = standardSpec("mimo-v2-pro", "Mimo V2 Pro", 1_048_576, 128_000, 1, 3, 0.2)
	specs["mimo-v2.5"] = standardSpec("mimo-v2.5", "Mimo V2.5", 1_000_000, 128_000, 0.14, 0.28, 0.0028)
	specs["mimo-v2.5-pro"] = standardSpec("mimo-v2.5-pro", "Mimo V2.5 Pro", 1_048_576, 128_000, 0.435, 0.87, 0.003625)
	// OpenCode Go exposes low through max without off or minimal
	// (Pi 0.84.1 opencode-go catalog).
	specs["gpt-5.6-luna"] = provider.ModelSpec{
		ID:            "gpt-5.6-luna",
		Name:          "GPT-5.6 Luna",
		ContextWindow: 1_050_000,
		MaxTokens:     128_000,
		Input:         0.1,
		Output:        0.6,
		CacheRead:     0.01,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelXHigh,
			llm.ThinkingLevelMax,
		),
	}
	// Grok 4.5 exposes low, medium, and high efforts
	// (Pi 0.84.1 opencode-go catalog).
	specs["grok-4.5"] = provider.ModelSpec{
		ID:            "grok-4.5",
		Name:          "Grok 4.5",
		ContextWindow: 500_000,
		MaxTokens:     500_000,
		Input:         2,
		Output:        6,
		CacheRead:     0.5,
		ThinkingLevelMap: llm.ThinkingLevelsMap(
			llm.ThinkingLevelLow,
			llm.ThinkingLevelMedium,
			llm.ThinkingLevelHigh,
		),
	}
	// Hy3 maps off to none and exposes low and high
	// (Pi 0.84.1 opencode-go catalog).
	specs["hy3"] = provider.ModelSpec{
		ID:            "hy3",
		Name:          "Hy3",
		ContextWindow: 256_000,
		MaxTokens:     64_000,
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

// qwenSpec builds a Qwen model spec with a standard five-level map and the
// top-level enable_thinking toggle used by OpenCode Go.
func qwenSpec(
	id, name string,
	contextWindow, maxTokens int64,
	input, output, cacheRead float64,
) provider.ModelSpec {
	spec := standardSpec(id, name, contextWindow, maxTokens, input, output, cacheRead)
	spec.ThinkingFormat = llm.ThinkingFormatQwen
	return spec
}

// modelIDs lists the OpenCode Go models in menu order; Models constructs
// fresh values so callers cannot mutate catalog maps shared by later runs.
var modelIDs = []string{
	"deepseek-v4-flash",
	"deepseek-v4-pro",
	"kimi-k2.5",
	"kimi-k2.6",
	"kimi-k2.7-code",
	"kimi-k3",
	"glm-5",
	"glm-5.1",
	"glm-5.2",
	"qwen3.5-plus",
	"qwen3.6-plus",
	"qwen3.7-plus",
	"qwen3.7-max",
	"qwen3.8-max",
	"minimax-m2.5",
	"minimax-m2.7",
	"minimax-m3",
	"mimo-v2-omni",
	"mimo-v2-pro",
	"mimo-v2.5",
	"mimo-v2.5-pro",
	"gpt-5.6-luna",
	"grok-4.5",
	"hy3",
}

func model(id string) llm.Model {
	spec := modelSpecs[id]
	return llm.Model{
		ID:                      id,
		Name:                    spec.Name,
		API:                     openaicompletions.API,
		Provider:                ProviderID,
		SupportsThinking:        true,
		ThinkingLevelMap:        spec.ThinkingLevelMap.Clone(),
		ThinkingFormat:          spec.ThinkingFormat,
		SupportsReasoningEffort: spec.SupportsReasoningEffort,
		OmitMaxTokensByDefault:  true,
		InputModalities:         []llm.InputModality{llm.InputModalityText},
		ContextWindow:           spec.ContextWindow,
		MaxTokens:               spec.MaxTokens,
		Pricing: llm.Pricing{
			Input:     spec.Input,
			Output:    spec.Output,
			CacheRead: spec.CacheRead,
		},
	}
}

// messageCapabilities declares the message content OpenCode Go models accept.
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
	return "OpenCode Go subscription (24 models via OpenAI Chat Completions)"
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
func (p *Provider) New(configuration config.Config) (agent.Model, error) {
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
