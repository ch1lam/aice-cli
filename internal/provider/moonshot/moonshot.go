// Package moonshot defines the Moonshot API Platform provider and model catalog.
package moonshot

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
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
// shared protocol adapters.
type Provider struct {
	responsesAdapter   *openairesponses.Adapter
	completionsAdapter *openaicompletions.Adapter
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
		Headers:    http.Header{"User-Agent": {"aice"}},
	})
	if err != nil {
		return nil, fmt.Errorf("moonshot: configure Responses adapter: %w", err)
	}
	completions, err := openaicompletions.New(openaicompletions.Config{
		APIKey: configuration.APIKey, BaseURL: baseURL, HTTPClient: configuration.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("moonshot: configure Chat Completions adapter: %w", err)
	}
	return &Provider{responsesAdapter: adapter, completionsAdapter: completions}, nil
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

// Stream validates Moonshot compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"moonshot: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if !knownModel(request.Model.ID) {
		return nil, fmt.Errorf("moonshot: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != modelAPI(request.Model.ID) {
		return nil, fmt.Errorf(
			"moonshot: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			modelAPI(request.Model.ID),
		)
	}
	if err := provider.ValidateMessages(request.Messages, messageCapabilities); err != nil {
		return nil, err
	}
	if request.Model.API == openairesponses.API {
		return p.responsesAdapter.Stream(ctx, request)
	}
	return p.completionsAdapter.Stream(ctx, request)
}

func knownModel(id string) bool {
	switch id {
	case ModelK3, ModelK27Code, ModelK27HighSpeed, ModelK26:
		return true
	default:
		return false
	}
}

func modelAPI(id string) llm.API {
	if id == ModelK3 {
		return openairesponses.API
	}
	return openaicompletions.API
}

// Capabilities follow https://platform.kimi.com/docs/api/models-overview.
func model(id, name string) llm.Model {
	levels := llm.ThinkingLevelsMap(llm.ThinkingLevelHigh)
	// K2.7 always thinks and rejects reasoning_effort. An empty wire token
	// keeps the supported high choice while omitting the effort field.
	levels[llm.ThinkingLevelHigh] = llm.ThinkingValue("")
	result := llm.Model{
		ID: id, Name: name, API: modelAPI(id), Provider: ProviderID,
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
		result.ThinkingFormat = llm.ThinkingFormatDeepSeek
	}
	return result
}

var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "Moonshot API",
	SupportsImage:            true,
	SupportsRedactedThinking: false,
	NestedToolResultTextOnly: false,
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

// Configured reports whether the configuration carries a Moonshot credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.MoonshotAPIKey != ""
}

// New constructs the credentialed Moonshot model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.MoonshotAPIKey,
		BaseURL: configuration.MoonshotBaseURL,
	})
}

// SaveAPIKey stores the Moonshot credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveMoonshotAPIKey(apiKey)
}

// ApplyAPIKey stores the Moonshot credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.MoonshotAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing Moonshot credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"Moonshot API key is not configured; run /login in interactive mode or set %s",
		config.EnvMoonshotAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
