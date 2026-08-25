// Package openai defines the official OpenAI API provider and model catalog.
package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	// ProviderID is OpenAI's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "openai"
	// BaseURL is OpenAI's official API root.
	BaseURL = "https://api.openai.com/v1"

	ModelGPT56      = "gpt-5.6"
	ModelGPT56Terra = "gpt-5.6-terra"
	ModelGPT56Luna  = "gpt-5.6-luna"
)

// Config contains OpenAI connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider validates OpenAI model compatibility before delegating to the
// shared Responses API adapter.
type Provider struct {
	responsesAdapter *openairesponses.Adapter
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs an OpenAI provider. An empty BaseURL selects the official
// API endpoint.
func New(configuration Config) (*Provider, error) {
	baseURL := BaseURL
	if strings.TrimSpace(configuration.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(configuration.BaseURL), "/")
	}
	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     configuration.APIKey,
		BaseURL:    baseURL,
		HTTPClient: configuration.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: configure Responses adapter: %w", err)
	}
	return &Provider{responsesAdapter: adapter}, nil
}

// Models returns the OpenAI models supported by this provider.
func Models() []llm.Model {
	return []llm.Model{
		model(ModelGPT56, "GPT-5.6", 5, 30, 0.5, 6.25),
		model(ModelGPT56Terra, "GPT-5.6 Terra", 2, 12, 0.2, 2.5),
		model(ModelGPT56Luna, "GPT-5.6 Luna", 1, 6, 0.1, 1.25),
	}
}

// DefaultModel returns OpenAI's balanced GPT-5.6 model.
func DefaultModel() llm.Model {
	return model(ModelGPT56Terra, "GPT-5.6 Terra", 2, 12, 0.2, 2.5)
}

// Stream validates OpenAI compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"openai: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if !knownModel(request.Model.ID) {
		return nil, fmt.Errorf("openai: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != openairesponses.API {
		return nil, fmt.Errorf(
			"openai: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			openairesponses.API,
		)
	}
	if err := provider.ValidateMessages(request.Messages, messageCapabilities); err != nil {
		return nil, err
	}
	return p.responsesAdapter.Stream(ctx, request)
}

func knownModel(id string) bool {
	switch id {
	case ModelGPT56, ModelGPT56Terra, ModelGPT56Luna:
		return true
	default:
		return false
	}
}

func model(id, name string, input, output, cacheRead, cacheWrite float64) llm.Model {
	return llm.Model{
		ID:               id,
		Name:             name,
		API:              openairesponses.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		ThinkingLevelMap: openAIThinkingLevels(),
		InputModalities: []llm.InputModality{
			llm.InputModalityText,
			llm.InputModalityImage,
		},
		ContextWindow: 1_050_000,
		MaxTokens:     128_000,
		Pricing: llm.Pricing{
			Input:      input,
			Output:     output,
			CacheRead:  cacheRead,
			CacheWrite: cacheWrite,
		},
	}
}

// openAIThinkingLevels declares the GPT-5.6 reasoning levels: off maps to the
// none effort, minimal is not exposed, and low through max use canonical
// effort tokens (models.dev effort metadata).
func openAIThinkingLevels() llm.ThinkingLevelMap {
	m := llm.ThinkingLevelsMap(
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	)
	m[llm.ThinkingLevelOff] = llm.ThinkingValue("none")
	return m
}

var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "OpenAI",
	SupportsImage:            true,
	SupportsRedactedThinking: false,
	NestedToolResultTextOnly: false,
}

// Label returns the OpenAI display name.
func (p *Provider) Label() string {
	return "OpenAI"
}

// MenuDescription describes OpenAI in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "OpenAI API (GPT-5.6 via Responses)"
}

// Models returns the OpenAI model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the OpenAI model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// Configured reports whether the configuration carries an OpenAI credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.OpenAIAPIKey != ""
}

// New constructs the credentialed OpenAI model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.OpenAIAPIKey,
		BaseURL: configuration.OpenAIBaseURL,
	})
}

// SaveAPIKey stores the OpenAI credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveOpenAIAPIKey(apiKey)
}

// ApplyAPIKey stores the OpenAI credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.OpenAIAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing OpenAI credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"OpenAI API key is not configured; run /login in interactive mode or set %s",
		config.EnvOpenAIAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
