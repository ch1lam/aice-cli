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

	ModelGPT62Astra = "gpt-6.2-astra"
	ModelGPT62Sol   = "gpt-6.2-sol"
	ModelGPT61Sol   = "gpt-6.1-sol"
	ModelGPT61Terra = "gpt-6.1-terra"
	ModelGPT6Astra  = "gpt-6-astra"
	ModelGPT6Sol    = "gpt-6-sol"
	ModelGPT6Luna   = "gpt-6-luna"
	ModelGPT56Sol   = "gpt-5.6-sol"
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
	route provider.Route
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
		APIKey:             configuration.APIKey,
		BaseURL:            baseURL,
		HTTPClient:         configuration.HTTPClient,
		EncryptedReasoning: true,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: configure Responses adapter: %w", err)
	}
	return &Provider{route: provider.Route{
		Provider: ProviderID,
		Label:    "OpenAI",
		Models:   Models(),
		Adapters: map[llm.API]llm.Streamer{openairesponses.API: adapter},
	}}, nil
}

// catalog lists current OpenAI Responses models, flagship first, with
// standard (short-context tier) USD prices per million tokens. off records
// whether the model accepts reasoning effort none; the GPT-6 Astra models,
// GPT-6.1 Sol and GPT-6.2 Sol reject it. Values follow pi's openai catalog
// and models.dev (checked 2026-11-25). The bare gpt-5.6 alias is omitted:
// the API rejects it.
var catalog = []struct {
	id, name string
	off      bool
	pricing  llm.Pricing
}{
	{ModelGPT62Astra, "GPT-6.2 Astra", false, llm.Pricing{Input: 8, Output: 40, CacheRead: 0.4, CacheWrite: 10}},
	{ModelGPT62Sol, "GPT-6.2 Sol", false, llm.Pricing{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
	{ModelGPT61Sol, "GPT-6.1 Sol", false, llm.Pricing{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
	{ModelGPT61Terra, "GPT-6.1 Terra", true, llm.Pricing{Input: 1, Output: 5, CacheRead: 0.05, CacheWrite: 1.25}},
	{ModelGPT6Astra, "GPT-6 Astra", false, llm.Pricing{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}},
	{ModelGPT6Sol, "GPT-6 Sol", true, llm.Pricing{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
	{ModelGPT6Luna, "GPT-6 Luna", true, llm.Pricing{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
	{ModelGPT56Sol, "GPT-5.6 Sol", true, llm.Pricing{Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5}},
	{ModelGPT56Terra, "GPT-5.6 Terra", true, llm.Pricing{Input: 2, Output: 12, CacheRead: 0.2, CacheWrite: 2.5}},
	{ModelGPT56Luna, "GPT-5.6 Luna", true, llm.Pricing{Input: 0.2, Output: 1.2, CacheRead: 0.02, CacheWrite: 0.25}},
}

// Models returns the OpenAI models supported by this provider.
func Models() []llm.Model {
	models := make([]llm.Model, 0, len(catalog))
	for _, entry := range catalog {
		models = append(models, llm.Model{
			ID:               entry.id,
			Name:             entry.name,
			API:              openairesponses.API,
			Provider:         ProviderID,
			SupportsThinking: true,
			ThinkingLevelMap: thinkingLevels(entry.off),
			InputModalities: []llm.InputModality{
				llm.InputModalityText,
				llm.InputModalityImage,
			},
			ContextWindow: 1_050_000,
			MaxTokens:     128_000,
			Pricing:       entry.pricing,
		})
	}
	return models
}

// DefaultModel returns GPT-6.2 Sol, the current general-purpose model.
func DefaultModel() llm.Model {
	for _, model := range Models() {
		if model.ID == ModelGPT62Sol {
			return model
		}
	}
	panic("openai: default model missing from catalog")
}

// Stream validates OpenAI compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
}

// thinkingLevels declares the GPT-5.6 and GPT-6 reasoning levels: off maps to
// the none effort where the model accepts it, minimal is rejected, and low
// through max use canonical effort tokens (models.dev effort metadata, pi's
// OpenAI family rules).
func thinkingLevels(off bool) llm.ThinkingLevelMap {
	m := llm.ThinkingLevelsMap(
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	)
	if off {
		m[llm.ThinkingLevelOff] = llm.ThinkingValue("none")
	}
	return m
}

// Label returns the OpenAI display name.
func (p *Provider) Label() string {
	return "OpenAI"
}

// MenuDescription describes OpenAI in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "OpenAI API (GPT-6.2, GPT-6.1, GPT-6 and GPT-5.6 via Responses)"
}

// Models returns the OpenAI model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the OpenAI model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// credential is the OpenAI API key in AICE configuration.
var credential = provider.APIKey{Provider: ProviderID, Label: "OpenAI"}

// Configured reports whether the configuration carries an API key.
func (p *Provider) Configured(c config.Config) bool { return credential.Configured(c) }

// New constructs the credentialed OpenAI model service.
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
