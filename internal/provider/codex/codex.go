// Package codex owns ChatGPT subscription authentication and model selection.
package codex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
)

const (
	ProviderID llm.ProviderID = "openai-codex"
	BaseURL                   = "https://chatgpt.com/backend-api/codex"
)

// Provider can be registered without credentials. Streams reread AICE's
// credentials under a file lock so concurrent runs never rotate stale tokens.
type Provider struct {
	paths   config.Paths
	auth    AuthClient
	baseURL string
	client  *http.Client
}

func (p *Provider) ProviderID() llm.ProviderID      { return ProviderID }
func (p *Provider) Label() string                   { return "OpenAI Codex" }
func (p *Provider) MenuDescription() string         { return "ChatGPT subscription (OAuth)" }
func (p *Provider) Models() []llm.Model             { return Models() }
func (p *Provider) DefaultModel() llm.Model         { return DefaultModel() }
func (p *Provider) Configured(c config.Config) bool { return c.CodexCredentials.Configured() }

// Models is deliberately independent of API billing prices and credentials.
// ContextWindow follows the default context_window in OpenAI's bundled
// codex-rs/models-manager/models.json, not max_context_window or API limits.
func Models() []llm.Model {
	var models []llm.Model
	for _, entry := range []struct{ id, name string }{
		{"gpt-6-astra", "GPT-6 Astra"},
		{"gpt-5.6-sol", "GPT-5.6 Sol"},
		{"gpt-5.6-terra", "GPT-5.6 Terra"},
		{"gpt-5.6-luna", "GPT-5.6 Luna"},
	} {
		models = append(models, llm.Model{
			ID: entry.id, Name: entry.name, API: openairesponses.API, Provider: ProviderID,
			SupportsThinking: true,
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelMedium,
				llm.ThinkingLevelHigh, llm.ThinkingLevelXHigh, llm.ThinkingLevelMax),
			InputModalities: []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			ContextWindow:   272_000, MaxTokens: 128_000,
		})
	}
	return models
}

func DefaultModel() llm.Model { return Models()[2] }

func (p *Provider) New(c config.Config) (llm.Streamer, error) {
	if !p.Configured(c) {
		return nil, p.CredentialNotConfiguredError()
	}
	return &Provider{paths: c.Paths, auth: AuthClient{}, baseURL: BaseURL}, nil
}

func (p *Provider) SaveAPIKey(string) (string, error) {
	return "", errors.New("Codex uses ChatGPT OAuth; run aice auth login --provider openai-codex")
}

// ApplyAPIKey cannot configure an OAuth provider; SaveAPIKey rejects that flow.
func (p *Provider) ApplyAPIKey(*config.Config, string) {}

func (p *Provider) CredentialNotConfiguredError() error {
	return errors.New("Codex subscription is not configured; run aice auth login --provider openai-codex")
}

func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if ctx == nil {
		return nil, errors.New("codex: context is required")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("codex: validate request: %w", err)
	}
	if request.Model.Provider != ProviderID || request.Model.API != openairesponses.API {
		return nil, errors.New("codex: incompatible model provider or API")
	}
	known := false
	for _, model := range Models() {
		if model.ID == request.Model.ID {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("codex: unsupported model %q", request.Model.ID)
	}
	if err := provider.ValidateMessages(request.Messages, provider.MessageCapabilities{
		ID: ProviderID, Label: "Codex", SupportsImage: true,
	}); err != nil {
		return nil, err
	}
	credential, err := config.UpdateCodexCredentials(ctx, p.paths, func(current config.CodexCredentials) (config.CodexCredentials, error) {
		if !current.Configured() {
			return config.CodexCredentials{}, p.CredentialNotConfiguredError()
		}
		if time.Until(current.ExpiresAt) > time.Minute {
			return current, nil
		}
		return p.auth.Refresh(ctx, current)
	})
	if err != nil {
		return nil, fmt.Errorf("codex: resolve credentials: %w", err)
	}
	client := &http.Client{}
	if p.client != nil {
		*client = *p.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	adapter, err := openairesponses.New(openairesponses.Config{
		APIKey: credential.AccessToken, BaseURL: p.baseURL, HTTPClient: client, Codex: true,
		Headers: http.Header{
			"Chatgpt-Account-Id": {credential.AccountID}, "Originator": {"aice"},
			"Openai-Beta": {"responses=experimental"},
			"Accept":      {"text/event-stream"},
		},
	})
	if err != nil {
		return nil, err
	}
	return adapter.Stream(ctx, request)
}

var _ provider.Provider = (*Provider)(nil)
