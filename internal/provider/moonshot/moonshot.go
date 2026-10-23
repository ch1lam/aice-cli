// Package moonshot defines the Moonshot API Platform provider and model catalog.
package moonshot

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
	// ProviderID is Moonshot's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "moonshot"
	// BaseURL is Moonshot's official API root.
	BaseURL = "https://api.moonshot.cn/v1"

	ModelK3           = "kimi-k3"
	ModelK27Code      = "kimi-k2.7-code"
	ModelK27HighSpeed = "kimi-k2.7-code-highspeed"
	ModelK26          = "kimi-k2.6"
)

// Config contains Moonshot connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider validates Moonshot model compatibility before delegating to the
// shared Responses API adapter.
type Provider struct {
	route provider.Route
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs a Moonshot provider. An empty BaseURL selects the official
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
		return nil, fmt.Errorf("moonshot: configure Responses adapter: %w", err)
	}
	return &Provider{route: provider.Route{
		Provider: ProviderID,
		Label:    "Moonshot API",
		Models:   Models(),
		Adapters: map[llm.API]llm.Streamer{openairesponses.API: adapter},
	}}, nil
}

// Models returns the Moonshot models supported by this provider.
func Models() []llm.Model {
	return []llm.Model{
		model(ModelK3, "Kimi K3"),
		model(ModelK27Code, "Kimi K2.7 Code"),
		model(ModelK27HighSpeed, "Kimi K2.7 Code HighSpeed"),
		model(ModelK26, "Kimi K2.6"),
	}
}

// DefaultModel returns the platform's flagship K3 model.
func DefaultModel() llm.Model { return model(ModelK3, "Kimi K3") }

// Stream validates compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
}

// Capabilities follow https://platform.kimi.com/docs/api/models-overview.
func model(id, name string) llm.Model {
	levels := llm.ThinkingLevelsMap(llm.ThinkingLevelHigh)
	result := llm.Model{
		ID: id, Name: name, API: openairesponses.API, Provider: ProviderID,
		SupportsThinking: true, ThinkingLevelMap: levels,
		InputModalities: []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
		ContextWindow:   262_144, MaxTokens: 32_768,
		// CNY platform prices are not represented by AICE's USD cost estimates.
	}
	switch id {
	case ModelK3:
		result.ContextWindow = 1_048_576
		result.MaxTokens = 131_072
		result.ThinkingLevelMap = llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax)
	case ModelK26:
		result.ThinkingLevelMap = llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh)
	}
	return result
}

// Label returns the Moonshot display name.
func (p *Provider) Label() string {
	return "Moonshot API"
}

// MenuDescription describes Moonshot in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "Moonshot API Platform (pay-as-you-go; China endpoint)"
}

// Models returns the Moonshot model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the Moonshot model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// credential is the Moonshot API key in AICE configuration.
var credential = provider.APIKey{Provider: ProviderID, Label: "Moonshot"}

// Configured reports whether the configuration carries an API key.
func (p *Provider) Configured(c config.Config) bool { return credential.Configured(c) }

// New constructs the credentialed Moonshot model service.
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
