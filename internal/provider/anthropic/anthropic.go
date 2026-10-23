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
	route provider.Route
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
	return &Provider{route: provider.Route{
		Provider:         ProviderID,
		Label:            "Anthropic",
		Models:           Models(),
		Adapters:         map[llm.API]llm.Streamer{anthropicapi.API: adapter},
		RedactedThinking: []llm.API{anthropicapi.API},
	}}, nil
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
	return p.route.Stream(ctx, request)
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

// credential is the Anthropic API key in AICE configuration.
var credential = provider.APIKey{Provider: ProviderID, Label: "Anthropic"}

// Configured reports whether the configuration carries an API key.
func (p *Provider) Configured(c config.Config) bool { return credential.Configured(c) }

// New constructs the credentialed Anthropic model service.
func (p *Provider) New(c config.Config) (llm.Streamer, error) {
	return New(Config{APIKey: credential.Value(c), BaseURL: credential.BaseURL(c)})
}

// SaveAPIKey stores the API key in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) { return credential.Save(apiKey) }

// ApplyAPIKey stores the API key in the configuration.
func (p *Provider) ApplyAPIKey(c *config.Config, apiKey string) { credential.Apply(c, apiKey) }

// CredentialNotConfiguredError describes the missing API key.
func (p *Provider) CredentialNotConfiguredError() error { return credential.NotConfiguredError() }

var _ provider.Provider = (*Provider)(nil)
