// Package zhipu defines the Zhipu API Platform provider and model catalog.
package zhipu

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
	// ProviderID is Zhipu's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "zhipu"
	// BaseURL is Zhipu's official API root.
	BaseURL = "https://open.bigmodel.cn/api/paas/v4"

	ModelGLM53 = "glm-5.3"
)

// Config contains Zhipu connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider validates Zhipu model compatibility before delegating to the
// shared Chat Completions API adapter.
type Provider struct {
	completionsAdapter *openaicompletions.Adapter
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	return ProviderID
}

// New constructs a Zhipu provider. An empty BaseURL selects the official
// API endpoint.
func New(configuration Config) (*Provider, error) {
	baseURL := BaseURL
	if strings.TrimSpace(configuration.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(configuration.BaseURL), "/")
	}
	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     configuration.APIKey,
		BaseURL:    baseURL,
		HTTPClient: configuration.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("zhipu: configure Chat Completions adapter: %w", err)
	}
	return &Provider{completionsAdapter: adapter}, nil
}

// Models returns the Zhipu models supported by this provider.
func Models() []llm.Model { return []llm.Model{DefaultModel()} }

// DefaultModel returns the GLM-5.3 coding model.
func DefaultModel() llm.Model { return model() }

// Stream validates Zhipu compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"zhipu: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if request.Model.ID != ModelGLM53 {
		return nil, fmt.Errorf("zhipu: unsupported model %q", request.Model.ID)
	}
	if request.Model.API != openaicompletions.API {
		return nil, fmt.Errorf(
			"zhipu: model %q API %q does not match %q",
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

// Capabilities follow https://docs.bigmodel.cn/cn/guide/models/text/glm-5.3.
func model() llm.Model {
	return llm.Model{
		ID: ModelGLM53, Name: "GLM-5.3", API: openaicompletions.API, Provider: ProviderID,
		SupportsThinking: true,
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
		ThinkingFormat:   llm.ThinkingFormatDeepSeek, SupportsReasoningEffort: true,
		InputModalities: []llm.InputModality{llm.InputModalityText},
		ContextWindow:   1_000_000, MaxTokens: 131_072,
		// CNY platform prices are not represented by AICE's USD cost estimates.
	}
}

var messageCapabilities = provider.MessageCapabilities{
	ID:                       ProviderID,
	Label:                    "Zhipu API",
	SupportsImage:            false,
	SupportsRedactedThinking: false,
	NestedToolResultTextOnly: true,
}

// Label returns the Zhipu display name.
func (p *Provider) Label() string {
	return "Zhipu API"
}

// MenuDescription describes Zhipu in interactive provider menus.
func (p *Provider) MenuDescription() string {
	return "Zhipu API Platform (pay-as-you-go; China endpoint)"
}

// Models returns the Zhipu model catalog.
func (p *Provider) Models() []llm.Model {
	return Models()
}

// DefaultModel returns the Zhipu model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	return DefaultModel()
}

// Configured reports whether the configuration carries a Zhipu credential.
func (p *Provider) Configured(configuration config.Config) bool {
	return configuration.ZhipuAPIKey != ""
}

// New constructs the credentialed Zhipu model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	return New(Config{
		APIKey:  configuration.ZhipuAPIKey,
		BaseURL: configuration.ZhipuBaseURL,
	})
}

// SaveAPIKey stores the Zhipu credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	return config.SaveZhipuAPIKey(apiKey)
}

// ApplyAPIKey stores the Zhipu credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	configuration.ZhipuAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing Zhipu credential.
func (p *Provider) CredentialNotConfiguredError() error {
	return fmt.Errorf(
		"Zhipu API key is not configured; run /login in interactive mode or set %s",
		config.EnvZhipuAPIKey,
	)
}

var _ provider.Provider = (*Provider)(nil)
