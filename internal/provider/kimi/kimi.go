// Package kimi defines the Kimi Coding Plan provider and model catalog.
package kimi

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
	// ProviderID is Kimi's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "kimi-coding"
	// BaseURL is Kimi's official API root.
	BaseURL = "https://api.kimi.com/coding/v1"

	ModelForCoding          = "kimi-for-coding"
	ModelForCodingHighSpeed = "kimi-for-coding-highspeed"
	ModelK3                 = "k3"
	ModelK3256K             = "k3-256k"
)

// Config contains Kimi connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider validates Kimi model compatibility before delegating to the
// shared Responses API adapter.
type Provider struct {
	route provider.Route
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs a Kimi provider. An empty BaseURL selects the official
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
		return nil, fmt.Errorf("kimi-coding: configure Responses adapter: %w", err)
	}
	return &Provider{route: provider.Route{
		Provider: ProviderID,
		Label:    "Kimi Coding Plan",
		Models:   Models(),
		Adapters: map[llm.API]llm.Streamer{openairesponses.API: adapter},
	}}, nil
}

// Models returns the Kimi models supported by this provider.
func Models() []llm.Model {
	return []llm.Model{
		model(ModelForCoding, "Kimi For Coding"),
		model(ModelForCodingHighSpeed, "Kimi For Coding HighSpeed"),
		model(ModelK3256K, "Kimi K3 256K"),
		model(ModelK3, "Kimi K3"),
	}
}

// DefaultModel returns the model available to all Coding Plan members.
func DefaultModel() llm.Model {
	return model(ModelForCoding, "Kimi For Coding")
}

// Stream validates compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
}

// Capabilities follow https://www.kimi.com/code/docs/en/kimi-code/models.html.
func model(id, name string) llm.Model {
	levels := llm.ThinkingLevelsMap(llm.ThinkingLevelHigh)
	if id == ModelK3 || id == ModelK3256K {
		levels = llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax)
	}
	return llm.Model{
		ID:               id,
		Name:             name,
		API:              openairesponses.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		ThinkingLevelMap: levels,
		InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
		// K3's 1M tier depends on membership. Use the common 256K limit;
		// entitled users can opt in through context_windows.
		ContextWindow: 262_144,
		// Conservative AICE output budget, not an advertised server maximum.
		MaxTokens: 32_768,
		// Subscription quota has no per-token API price.
	}
}

// Label returns the Kimi display name.
func (p *Provider) Label() string {
	return "Kimi Coding Plan"
}

// MenuDescription describes Kimi in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "Kimi Coding Plan subscription (Responses API)"
}

// Models returns the Kimi model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the Kimi model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// credential is the Kimi API key in AICE configuration.
var credential = provider.APIKey{Provider: ProviderID, Label: "Kimi"}

// Configured reports whether the configuration carries an API key.
func (p *Provider) Configured(c config.Config) bool { return credential.Configured(c) }

// New constructs the credentialed Kimi model service.
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
