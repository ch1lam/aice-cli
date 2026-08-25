// Package custom implements a generic OpenAI-compatible provider.
//
// It is the catch-all provider for Ollama, vLLM, LM Studio, or any
// OpenAI-compatible endpoint. Like Pi's models.json custom providers, the
// model catalog is not compiled in: any model ID is accepted and materialized
// on the fly with safe defaults. The wire protocol is always
// openai-completions (POST {baseURL}/chat/completions streamed via SSE).
package custom

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	// ProviderID is the identifier used in AICE configuration.
	ProviderID llm.ProviderID = "custom"
	// DefaultBaseURL is Ollama's default OpenAI-compatible endpoint. It is
	// used when no custom base URL is configured so selecting the custom
	// provider via AICE_PROVIDER, settings.json, or TUI /provider and /login
	// works out of the box for local Ollama.
	DefaultBaseURL = "http://localhost:11434/v1"
)

// Config contains transport settings for the generic endpoint.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider is a thin compatibility layer over the Chat Completions adapter.
// It accepts any model ID and does not enforce a static catalog.
type Provider struct {
	completionsAdapter *openaicompletions.Adapter
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs a generic OpenAI-compatible provider. An empty BaseURL
// selects DefaultBaseURL. An empty APIKey is replaced with a dummy value
// because Ollama and other local servers ignore credentials yet the
// completions adapter requires a non-empty key.
func New(cfg Config) (*Provider, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = "ollama"
	}

	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		HTTPClient: cfg.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("custom: configure completions adapter: %w", err)
	}
	return &Provider{completionsAdapter: adapter}, nil
}

// ModelForID builds a provider-neutral model for an arbitrary ID. This is the
// Pi-inspired behavior: `models.json` requires only `id`, all other fields
// fall back to safe defaults. The custom provider never rejects an unknown ID.
//
// Thinking defaults to the standard off-through-high spectrum so the global
// `medium` default is respected and `/thinking` can be adjusted live. Servers
// that ignore `reasoning_effort` (e.g. base Ollama) simply ignore the field.
func ModelForID(id string) llm.Model {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "default"
	}
	return llm.Model{
		ID:               id,
		Name:             id,
		API:              openaicompletions.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		ThinkingLevelMap: llm.StandardThinkingLevelMap(),
		InputModalities:  []llm.InputModality{llm.InputModalityText},
		ContextWindow:    128_000,
		MaxTokens:        16_384,
		Pricing:          llm.Pricing{},
	}
}

// Models returns the catalog. The generic provider has no compiled catalog
// by design: it materializes any ID on the fly. A small set of well-known
// local tags is returned for menu display, but any model ID from AICE_MODEL,
// settings.json, or TUI /model is accepted via ModelForID.
func Models() []llm.Model {
	return []llm.Model{
		ModelForID("llama3.1:8b"),
		ModelForID("qwen3:8b"),
		ModelForID("gpt-oss:20b"),
	}
}

// DefaultModel returns the model used when none is selected.
func DefaultModel() llm.Model {
	return ModelForID("llama3.1:8b")
}

// Stream validates compatibility and delegates to the completions adapter.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"custom: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if strings.TrimSpace(request.Model.ID) == "" {
		return nil, fmt.Errorf("custom: model id is required")
	}
	if request.Model.API != openaicompletions.API {
		return nil, fmt.Errorf(
			"custom: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			openaicompletions.API,
		)
	}
	if err := provider.ValidateMessages(request.Messages, messageCapabilities); err != nil {
		return nil, err
	}
	return p.completionsAdapter.Stream(ctx, request)
}

var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "Custom",
	SupportsImage:            false,
	SupportsRedactedThinking: false,
	NestedToolResultTextOnly: false,
}

// Label returns the display name.
func (p *Provider) Label() string {
	return "Custom"
}

// MenuDescription describes the provider in interactive menus.
func (p *Provider) MenuDescription() string {
	return "Custom OpenAI-compatible API (Ollama, vLLM, LM Studio) · /login custom [endpoint] or " + config.EnvCustomBaseURL
}

// Models returns the provider's model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// Configured reports whether the provider can be used. The custom provider is
// keyless by design (Ollama ignores credentials), so it is always considered
// configured. A missing or unreachable endpoint surfaces as a request error
// rather than a credential error, matching Pi's dummy-key behavior.
func (p *Provider) Configured(_ config.Config) bool {
	return true
}

// New constructs the credentialed model service from global configuration.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.CustomAPIKey,
		BaseURL: configuration.CustomBaseURL,
	})
}

// SaveAPIKey stores the custom credential in the global auth file. An empty
// value is allowed to clear the entry for keyless local servers.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveCustomAPIKey(apiKey)
}

// ApplyAPIKey stores the credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.CustomAPIKey = apiKey
}

// CredentialNotConfiguredError is never returned for the custom provider
// because it is always configured. It is retained to satisfy the interface.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"Custom API key is not configured; set %s or %s",
		config.EnvCustomAPIKey,
		config.EnvCustomBaseURL,
	)
}

var _ provider.Provider = (*Provider)(nil)
