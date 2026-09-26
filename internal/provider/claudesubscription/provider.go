// Package claudesubscription owns Claude subscription authentication and model selection.
package claudesubscription

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	anthropicapi "github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	anthropicprovider "github.com/ch1lam/aice-cli/internal/provider/anthropic"
)

const (
	ProviderID llm.ProviderID = "anthropic-subscription"
	BaseURL                   = "https://api.anthropic.com"
)

// Provider can be registered without credentials. Streams reread AICE's
// credentials under a file lock so concurrent runs never rotate stale tokens.
type Provider struct {
	paths   config.Paths
	auth    AuthClient
	baseURL string
	client  *http.Client
}

func (p *Provider) ProviderID() llm.ProviderID { return ProviderID }
func (p *Provider) Label() string              { return "Claude Pro/Max" }
func (p *Provider) MenuDescription() string    { return "Claude subscription (native OAuth)" }
func (p *Provider) Models() []llm.Model        { return Models() }
func (p *Provider) DefaultModel() llm.Model    { return DefaultModel() }
func (p *Provider) Configured(c config.Config) bool {
	return c.ClaudeSubscriptionCredentials.Configured()
}

// Models shares Claude capability facts with the API catalog but keeps
// provider identity and subscription billing independent.
func Models() []llm.Model {
	models := anthropicprovider.Models()
	for i := range models {
		models[i].Provider = ProviderID
		models[i].Pricing = llm.Pricing{}
	}
	return models
}

func DefaultModel() llm.Model { return Models()[0] }

func (p *Provider) New(c config.Config) (llm.Streamer, error) {
	if !p.Configured(c) {
		return nil, p.CredentialNotConfiguredError()
	}
	return &Provider{paths: c.Paths, auth: AuthClient{}, baseURL: BaseURL}, nil
}

func (p *Provider) SaveAPIKey(string) (string, error) {
	return "", errors.New("Claude subscriptions use OAuth; run aice auth login --provider anthropic-subscription")
}

// ApplyAPIKey cannot configure an OAuth provider; SaveAPIKey rejects that flow.
func (p *Provider) ApplyAPIKey(*config.Config, string) {}

func (p *Provider) CredentialNotConfiguredError() error {
	return errors.New("Claude subscription is not configured; run aice auth login --provider anthropic-subscription")
}

func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if ctx == nil {
		return nil, errors.New("claude-subscription: context is required")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("claude-subscription: validate request: %w", err)
	}
	if request.Model.Provider != ProviderID || request.Model.API != anthropicapi.API {
		return nil, errors.New("claude-subscription: incompatible model provider or API")
	}
	known := false
	for _, model := range Models() {
		if model.ID == request.Model.ID {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("claude-subscription: unsupported model %q", request.Model.ID)
	}
	if err := provider.ValidateMessages(request.Messages, provider.MessageCapabilities{
		ID: ProviderID, Label: "Claude subscription", SupportsImage: true, SupportsRedactedThinking: true,
	}); err != nil {
		return nil, err
	}
	credential, err := config.UpdateClaudeSubscriptionCredentials(ctx, p.paths, func(current config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
		if !current.Configured() {
			return config.ClaudeSubscriptionCredentials{}, p.CredentialNotConfiguredError()
		}
		if time.Until(current.ExpiresAt) > 5*time.Minute {
			return current, nil
		}
		return p.auth.Refresh(ctx, current)
	})
	if err != nil {
		return nil, fmt.Errorf("claude-subscription: resolve credentials: %w", err)
	}
	client := &http.Client{}
	if p.client != nil {
		*client = *p.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	adapter, err := anthropicapi.New(anthropicapi.Config{
		OAuthToken: credential.AccessToken, BaseURL: p.baseURL, HTTPClient: client,
	})
	if err != nil {
		return nil, err
	}
	return adapter.Stream(ctx, request)
}

var _ provider.Provider = (*Provider)(nil)
