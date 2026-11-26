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

	ModelK31Pro          = "kimi-k3.1-pro"
	ModelK31ProHighSpeed = "kimi-k3.1-pro-highspeed"
	ModelK3              = "kimi-k3"
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
		model(ModelK31Pro, "Kimi K3.1 Pro"),
		model(ModelK31ProHighSpeed, "Kimi K3.1 Pro HighSpeed"),
		model(ModelK3, "Kimi K3"),
	}
}

// DefaultModel returns the platform's flagship K3.1 Pro model.
func DefaultModel() llm.Model { return model(ModelK31Pro, "Kimi K3.1 Pro") }

// Stream validates compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
}

// Capabilities follow https://platform.kimi.com/docs/api/responses and
// https://platform.kimi.com/docs/api/models-overview (checked 2026-11-26).
// /v1/responses serves only the K3 family with a 1M context window and
// canonical 131,072-token output budget; reasoning is always on and accepts
// low, high, and max.
func model(id, name string) llm.Model {
	return llm.Model{
		ID:               id,
		Name:             name,
		API:              openairesponses.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
		InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
		ContextWindow:    1_048_576,
		MaxTokens:        131_072,
		// CNY platform prices are not represented by AICE's USD cost estimates.
	}
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
