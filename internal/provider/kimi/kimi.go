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
	responsesAdapter *openairesponses.Adapter
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
	return &Provider{responsesAdapter: adapter}, nil
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

// Stream validates Kimi compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"kimi-coding: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if !knownModel(request.Model.ID) {
		return nil, fmt.Errorf("kimi-coding: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != openairesponses.API {
		return nil, fmt.Errorf(
			"kimi-coding: model %q API %q does not match %q",
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
	case ModelForCoding, ModelForCodingHighSpeed, ModelK3, ModelK3256K:
		return true
	default:
		return false
	}
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

var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "Kimi Coding Plan",
	SupportsImage:            true,
	SupportsRedactedThinking: false,
	NestedToolResultTextOnly: false,
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

// Configured reports whether the configuration carries a Kimi credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.KimiAPIKey != ""
}

// New constructs the credentialed Kimi model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.KimiAPIKey,
		BaseURL: configuration.KimiBaseURL,
	})
}

// SaveAPIKey stores the Kimi credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveKimiAPIKey(apiKey)
}

// ApplyAPIKey stores the Kimi credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.KimiAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing Kimi credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"Kimi API key is not configured; run /login in interactive mode or set %s",
		config.EnvKimiAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
