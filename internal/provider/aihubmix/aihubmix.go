// Package aihubmix defines the AiHubMix gateway catalog and protocol routing.
package aihubmix

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	// ProviderID identifies AiHubMix in AICE configuration.
	ProviderID llm.ProviderID = "aihubmix"
	// BaseURL is the gateway API root, including its version prefix.
	BaseURL = "https://aihubmix.com/v1"
)

// Config contains AiHubMix credentials and transport settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider delegates each catalog model to its supported protocol adapter.
type Provider struct {
	messages    *anthropic.Adapter
	responses   *openairesponses.Adapter
	completions *openaicompletions.Adapter
}

// New constructs a provider; an empty BaseURL selects the official gateway.
func New(cfg Config) (*Provider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}
	key := strings.TrimSpace(cfg.APIKey)
	responses, err := openairesponses.New(openairesponses.Config{
		APIKey: key, BaseURL: baseURL, HTTPClient: cfg.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("aihubmix: configure Responses adapter: %w", err)
	}
	messages, err := anthropic.New(anthropic.Config{
		APIKey: key, BaseURL: strings.TrimSuffix(baseURL, "/v1"), HTTPClient: cfg.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("aihubmix: configure Messages adapter: %w", err)
	}
	completions, err := openaicompletions.New(openaicompletions.Config{
		APIKey: key, BaseURL: baseURL, HTTPClient: cfg.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("aihubmix: configure Chat Completions adapter: %w", err)
	}
	return &Provider{messages: messages, responses: responses, completions: completions}, nil
}

// Stream validates model identity and content before opening a remote stream.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf("aihubmix: model provider %q does not match %q", request.Model.Provider, ProviderID)
	}
	expected, ok := catalogModel(request.Model.ID)
	if !ok {
		return nil, fmt.Errorf("aihubmix: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != expected.API {
		return nil, fmt.Errorf(
			"aihubmix: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			expected.API,
		)
	}
	if err := provider.ValidateMessages(request.Messages, provider.MessageCapabilities{
		ID: ProviderID, Label: "AiHubMix", SupportsImage: true,
		SupportsRedactedThinking: expected.API == anthropic.API,
	}); err != nil {
		return nil, err
	}
	switch expected.API {
	case anthropic.API:
		return p.messages.Stream(ctx, request)
	case openairesponses.API:
		return p.responses.Stream(ctx, request)
	default:
		return p.completions.Stream(ctx, request)
	}
}

// ProviderID reports the provider identity.
func (p *Provider) ProviderID() llm.ProviderID { return ProviderID }

// Label returns the display name.
func (p *Provider) Label() string { return "AiHubMix" }

// MenuDescription describes the gateway in interactive menus.
func (p *Provider) MenuDescription() string {
	return "AiHubMix model gateway (GPT, Claude, DeepSeek, Kimi)"
}

// Models returns the provider catalog.
func (p *Provider) Models() []llm.Model { return Models() }

// DefaultModel returns the default coding model.
func (p *Provider) DefaultModel() llm.Model { return DefaultModel() }

// Configured checks for an AiHubMix credential.
func (p *Provider) Configured(c config.Config) bool { return strings.TrimSpace(c.AiHubMixAPIKey) != "" }

// New constructs a model service from the effective configuration.
func (p *Provider) New(c config.Config) (llm.Streamer, error) {
	return New(Config{APIKey: c.AiHubMixAPIKey, BaseURL: c.AiHubMixBaseURL})
}

// SaveAPIKey persists the credential in the global auth file.
func (p *Provider) SaveAPIKey(key string) (string, error) { return config.SaveAiHubMixAPIKey(key) }

// ApplyAPIKey applies a credential to a configuration snapshot.
func (p *Provider) ApplyAPIKey(c *config.Config, key string) { c.AiHubMixAPIKey = key }

// CredentialNotConfiguredError explains how to configure access.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf("AiHubMix API key is not configured; run /login in interactive mode or set %s", config.EnvAiHubMixAPIKey)
}

var _ provider.Provider = (*Provider)(nil)
