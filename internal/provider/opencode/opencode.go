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
	specs["kimi-k2.5"] = provider.ModelSpec{ID: "kimi-k2.5", Name: "Kimi K2.5", ContextWindow: 262_144, MaxTokens: 65_536, Input: 0.6, Output: 3, CacheRead: 0.1}
	specs["kimi-k2.6"] = provider.ModelSpec{ID: "kimi-k2.6", Name: "Kimi K2.6", ContextWindow: 262_144, MaxTokens: 65_536, Input: 0.95, Output: 4, CacheRead: 0.16}
	specs["kimi-k2.7-code"] = provider.ModelSpec{ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", ContextWindow: 262_144, MaxTokens: 262_144, Input: 0.95, Output: 4, CacheRead: 0.19}
	specs["kimi-k3"] = provider.ModelSpec{ID: "kimi-k3", Name: "Kimi K3", ContextWindow: 1_048_576, MaxTokens: 131_072, Input: 3, Output: 15, CacheRead: 0.3}
	specs["glm-5"] = provider.ModelSpec{ID: "glm-5", Name: "GLM-5", ContextWindow: 202_752, MaxTokens: 32_768, Input: 1, Output: 3.2, CacheRead: 0.2}
	specs["glm-5.1"] = provider.ModelSpec{ID: "glm-5.1", Name: "GLM-5.1", ContextWindow: 202_752, MaxTokens: 32_768, Input: 1.4, Output: 4.4, CacheRead: 0.26}
	specs["glm-5.2"] = provider.ModelSpec{ID: "glm-5.2", Name: "GLM-5.2", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 1.4, Output: 4.4, CacheRead: 0.26}
	specs["qwen3.5-plus"] = provider.ModelSpec{ID: "qwen3.5-plus", Name: "Qwen 3.5 Plus", ContextWindow: 262_144, MaxTokens: 65_536, Input: 0.2, Output: 1.2, CacheRead: 0.02}
	specs["qwen3.6-plus"] = provider.ModelSpec{ID: "qwen3.6-plus", Name: "Qwen 3.6 Plus", ContextWindow: 1_000_000, MaxTokens: 65_536, Input: 0.5, Output: 3, CacheRead: 0.05}
	specs["qwen3.7-plus"] = provider.ModelSpec{ID: "qwen3.7-plus", Name: "Qwen 3.7 Plus", ContextWindow: 1_000_000, MaxTokens: 65_536, Input: 0.4, Output: 1.6, CacheRead: 0.04}
	specs["qwen3.7-max"] = provider.ModelSpec{ID: "qwen3.7-max", Name: "Qwen 3.7 Max", ContextWindow: 1_000_000, MaxTokens: 65_536, Input: 2.5, Output: 7.5, CacheRead: 0.5}
	specs["qwen3.8-max"] = provider.ModelSpec{ID: "qwen3.8-max", Name: "Qwen 3.8 Max", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 2, Output: 6, CacheRead: 0.25}
	specs["minimax-m2.5"] = provider.ModelSpec{ID: "minimax-m2.5", Name: "MiniMax M2.5", ContextWindow: 204_800, MaxTokens: 65_536, Input: 0.3, Output: 1.2, CacheRead: 0.03}
	specs["minimax-m2.7"] = provider.ModelSpec{ID: "minimax-m2.7", Name: "MiniMax M2.7", ContextWindow: 204_800, MaxTokens: 131_072, Input: 0.3, Output: 1.2, CacheRead: 0.06}
	specs["minimax-m3"] = provider.ModelSpec{ID: "minimax-m3", Name: "MiniMax M3", ContextWindow: 1_000_000, MaxTokens: 131_072, Input: 0.3, Output: 1.2, CacheRead: 0.06}
	specs["mimo-v2-omni"] = provider.ModelSpec{ID: "mimo-v2-omni", Name: "Mimo V2 Omni", ContextWindow: 262_144, MaxTokens: 128_000, Input: 0.4, Output: 2, CacheRead: 0.08}
	specs["mimo-v2-pro"] = provider.ModelSpec{ID: "mimo-v2-pro", Name: "Mimo V2 Pro", ContextWindow: 1_048_576, MaxTokens: 128_000, Input: 1, Output: 3, CacheRead: 0.2}
	specs["mimo-v2.5"] = provider.ModelSpec{ID: "mimo-v2.5", Name: "Mimo V2.5", ContextWindow: 1_000_000, MaxTokens: 128_000, Input: 0.14, Output: 0.28, CacheRead: 0.0028}
	specs["mimo-v2.5-pro"] = provider.ModelSpec{ID: "mimo-v2.5-pro", Name: "Mimo V2.5 Pro", ContextWindow: 1_048_576, MaxTokens: 128_000, Input: 0.435, Output: 0.87, CacheRead: 0.003625}
	specs["gpt-5.6-luna"] = provider.ModelSpec{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", ContextWindow: 1_050_000, MaxTokens: 128_000, Input: 0.1, Output: 0.6, CacheRead: 0.01}
	specs["grok-4.5"] = provider.ModelSpec{ID: "grok-4.5", Name: "Grok 4.5", ContextWindow: 500_000, MaxTokens: 500_000, Input: 2, Output: 6, CacheRead: 0.5}
	specs["hy3"] = provider.ModelSpec{ID: "hy3", Name: "Hy3", ContextWindow: 256_000, MaxTokens: 64_000, Input: 0.14, Output: 0.58, CacheRead: 0.035}
	return specs
}

// catalog lists the OpenCode Go models in menu order; the DeepSeek models
// share the specs declared in the provider package.
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
		Name:             spec.Name,
		API:              openaicompletions.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		InputModalities:  []llm.InputModality{llm.InputModalityText},
		ContextWindow:    spec.ContextWindow,
		MaxTokens:        spec.MaxTokens,
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
