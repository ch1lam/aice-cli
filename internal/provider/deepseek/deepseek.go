// Package deepseek defines DeepSeek models and compatibility behavior.
package deepseek

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	// ProviderID is DeepSeek's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "deepseek"
	// BaseURL is DeepSeek's OpenAI-compatible API root.
	BaseURL = "https://api.deepseek.com"
	// AnthropicBaseURL is DeepSeek's Anthropic-compatible API root.
	AnthropicBaseURL = BaseURL + "/anthropic"

	ModelV4Flash = "deepseek-flash"
	ModelV4Pro   = "deepseek-v4-pro"
)

// Config contains DeepSeek connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider applies DeepSeek compatibility rules before delegating to the wire
// protocol selected by the request model.
type Provider struct {
	route provider.Route
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs a DeepSeek provider. An empty BaseURL selects the official
// protocol endpoints. A custom Anthropic endpoint ending in /anthropic is also
// used to derive the sibling Responses endpoint.
func New(config Config) (*Provider, error) {
	anthropicBaseURL, responsesBaseURL := protocolBaseURLs(config.BaseURL)

	anthropicAdapter, err := anthropic.New(anthropic.Config{
		APIKey:     config.APIKey,
		BaseURL:    anthropicBaseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseek: configure Anthropic adapter: %w", err)
	}
	responsesAdapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     config.APIKey,
		BaseURL:    responsesBaseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseek: configure Responses adapter: %w", err)
	}
	return &Provider{route: provider.Route{
		Provider: ProviderID,
		Label:    "DeepSeek",
		Models:   Models(),
		Adapters: map[llm.API]llm.Streamer{
			anthropic.API:       anthropicAdapter,
			openairesponses.API: responsesAdapter,
		},
		NestedToolResultTextOnly: true,
	}}, nil
}

// Models returns the DeepSeek models supported by this provider.
func Models() []llm.Model {
	return []llm.Model{model(ModelV4Flash), model(ModelV4Pro)}
}

// DefaultModel returns the fast model used when no explicit model is selected.
func DefaultModel() llm.Model {
	return model(ModelV4Flash)
}

// Stream validates provider-specific compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
}

func model(id string) llm.Model {
	// OpenCode Go still uses the shared V4 Flash identity and metadata.
	specID := id
	if id == ModelV4Flash {
		specID = "deepseek-v4-flash"
	}
	var spec provider.ModelSpec
	for _, candidate := range provider.DeepSeekModelSpecs() {
		if candidate.ID == specID {
			spec = candidate
			break
		}
	}
	if id == ModelV4Flash {
		spec.Name = "DeepSeek Flash"
	}
	api := openairesponses.API
	if id == ModelV4Pro {
		api = anthropic.API
	}
	return llm.Model{
		ID:                      id,
		Name:                    spec.Name,
		API:                     api,
		Provider:                ProviderID,
		SupportsThinking:        true,
		ThinkingLevelMap:        spec.ThinkingLevelMap,
		SupportsReasoningEffort: id == ModelV4Pro,
		InputModalities:         inputModalities(id),
		ContextWindow:           spec.ContextWindow,
		MaxTokens:               spec.MaxTokens,
		Pricing: llm.Pricing{
			Input:     spec.Input,
			Output:    spec.Output,
			CacheRead: spec.CacheRead,
		},
	}
}

func protocolBaseURLs(configured string) (string, string) {
	configured = strings.TrimRight(strings.TrimSpace(configured), "/")
	if configured == "" {
		return AnthropicBaseURL, BaseURL
	}

	responsesBaseURL := configured
	if strings.HasSuffix(responsesBaseURL, "/anthropic") {
		responsesBaseURL = strings.TrimSuffix(responsesBaseURL, "/anthropic")
		return configured, responsesBaseURL
	}
	return configured + "/anthropic", responsesBaseURL
}

// inputModalities reports the content a DeepSeek model accepts. Only Flash
// (V4.1-Flash) supports native vision; V4 Pro is text-only.
func inputModalities(id string) []llm.InputModality {
	if id == ModelV4Pro {
		return []llm.InputModality{llm.InputModalityText}
	}
	return []llm.InputModality{llm.InputModalityText, llm.InputModalityImage}
}

// Label returns the DeepSeek display name.
func (p *Provider) Label() string {
	return "DeepSeek"
}

// MenuDescription describes DeepSeek in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "DeepSeek API (Flash via Responses, V4 Pro via Anthropic Messages)"
}

// Models returns the DeepSeek model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the DeepSeek model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// Configured reports whether the configuration carries a DeepSeek credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.DeepSeekAPIKey != ""
}

// New constructs the credentialed DeepSeek model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.DeepSeekAPIKey,
		BaseURL: configuration.DeepSeekBaseURL,
	})
}

// SaveAPIKey stores the DeepSeek credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveDeepSeekAPIKey(apiKey)
}

// ApplyAPIKey stores the DeepSeek credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.DeepSeekAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing DeepSeek credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"DeepSeek API key is not configured; run /login in interactive mode or set %s",
		config.EnvDeepSeekAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
