// Package anthropic defines the official Anthropic API provider and model catalog.
package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	anthropicapi "github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	// ProviderID is Anthropic's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "anthropic"
	// BaseURL is Anthropic's official API root.
	BaseURL = "https://api.anthropic.com"

	ModelSonnet5 = "claude-sonnet-5"
	ModelOpus55  = "claude-opus-5-5"
	ModelFable51 = "claude-fable-5-1"
	ModelHaiku45 = "claude-haiku-4-5-20251001"
)

// Config contains Anthropic connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider validates Anthropic model compatibility before delegating to the
// shared Messages API adapter.
type Provider struct {
	messagesAdapter *anthropicapi.Adapter
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs an Anthropic provider. An empty BaseURL selects the official
// API endpoint.
func New(configuration Config) (*Provider, error) {
	baseURL := BaseURL
	if strings.TrimSpace(configuration.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(configuration.BaseURL), "/")
	}
	adapter, err := anthropicapi.New(anthropicapi.Config{
		APIKey:     configuration.APIKey,
		BaseURL:    baseURL,
		HTTPClient: configuration.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: configure Messages adapter: %w", err)
	}
	return &Provider{messagesAdapter: adapter}, nil
}

// Models returns independent catalog values from Anthropic's model and effort
// documentation (2026-09-26). Cache write estimates use the five-minute rate.
func Models() []llm.Model {
	return []llm.Model{
		adaptiveModel(ModelSonnet5, "Claude Sonnet 5", llm.Pricing{Input: 2, Output: 10, CacheRead: .2, CacheWrite: 2.5}, true),
		adaptiveModel(ModelOpus55, "Claude Opus 5.5", llm.Pricing{Input: 4, Output: 20, CacheRead: .2, CacheWrite: 5}, false),
		adaptiveModel(ModelFable51, "Claude Fable 5.1", llm.Pricing{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}, false),
		{
			ID: ModelHaiku45, Name: "Claude Haiku 4.5", Provider: ProviderID, API: anthropicapi.API,
			ContextWindow: 200_000, MaxTokens: 64_000,
			InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			SupportsThinking: true,
			// Extended thinking uses the adapter's 1,024-token budget; Haiku has no effort parameter.
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh),
			Pricing:          llm.Pricing{Input: 1, Output: 5, CacheRead: .1, CacheWrite: 1.25},
		},
	}
}

// DefaultModel selects Claude Sonnet 5.
func DefaultModel() llm.Model { return Models()[0] }

// Stream validates Anthropic compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"anthropic: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if !knownModel(request.Model.ID) {
		return nil, fmt.Errorf("anthropic: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != anthropicapi.API {
		return nil, fmt.Errorf(
			"anthropic: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			anthropicapi.API,
		)
	}
	if err := provider.ValidateMessages(request.Messages, messageCapabilities); err != nil {
		return nil, err
	}
	return p.messagesAdapter.Stream(ctx, request)
}

func knownModel(id string) bool {
	switch id {
	case ModelSonnet5, ModelOpus55, ModelFable51, ModelHaiku45:
		return true
	default:
		return false
	}
}

func adaptiveModel(id, name string, pricing llm.Pricing, supportsOff bool) llm.Model {
	levels := llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax)
	if supportsOff {
		levels[llm.ThinkingLevelOff] = llm.ThinkingValue("off")
	}
	return llm.Model{
		ID: id, Name: name, Provider: ProviderID, API: anthropicapi.API,
		ContextWindow: 1_000_000, MaxTokens: 128_000,
		InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
		SupportsThinking: true, ThinkingLevelMap: levels,
		ThinkingFormat: llm.ThinkingFormatAdaptive, SupportsReasoningEffort: true,
		Pricing: pricing,
	}
}

var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "Anthropic",
	SupportsImage:            true,
	SupportsRedactedThinking: true,
	NestedToolResultTextOnly: false,
}

// Label returns the Anthropic display name.
func (p *Provider) Label() string {
	return "Anthropic (Claude API)"
}

// MenuDescription describes Anthropic in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "Claude API (separately billed Anthropic Messages)"
}

// Models returns the Anthropic model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the Anthropic model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// Configured reports whether the configuration carries an Anthropic credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.AnthropicAPIKey != ""
}

// New constructs the credentialed Anthropic model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.AnthropicAPIKey,
		BaseURL: configuration.AnthropicBaseURL,
	})
}

// SaveAPIKey stores the Anthropic credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveAnthropicAPIKey(apiKey)
}

// ApplyAPIKey stores the Anthropic credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.AnthropicAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing Anthropic credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"Anthropic API key is not configured; run /login in interactive mode or set %s",
		config.EnvAnthropicAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
