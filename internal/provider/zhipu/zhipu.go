// Package zhipu defines the Zhipu API Platform and Coding Plan providers
// and their model catalog.
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

	// CodingProviderID selects the separately credentialed Coding Plan endpoint.
	CodingProviderID llm.ProviderID = "zhipu-coding"
	CodingBaseURL                   = "https://open.bigmodel.cn/api/coding/paas/v4"

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
	codingPlan         bool
	completionsAdapter *openaicompletions.Adapter
}

// ProviderID reports the provider identity served by this provider.
func (p *Provider) ProviderID() llm.ProviderID {
	if p.codingPlan {
		return CodingProviderID
	}
	return ProviderID
}

// New constructs a Zhipu provider. An empty BaseURL selects the official
// API endpoint.
func New(configuration Config) (*Provider, error) {
	return newProvider(configuration, false)
}

// CodingPlan returns the uncredentialed descriptor used by the app registry.
func CodingPlan() *Provider { return &Provider{codingPlan: true} }

// NewCoding constructs a service that exclusively uses Coding Plan credentials
// and endpoints. It never falls back to the pay-as-you-go API.
func NewCoding(configuration Config) (*Provider, error) {
	return newProvider(configuration, true)
}

func newProvider(configuration Config, codingPlan bool) (*Provider, error) {
	descriptor := &Provider{codingPlan: codingPlan}
	baseURL := BaseURL
	if codingPlan {
		baseURL = CodingBaseURL
	}
	if strings.TrimSpace(configuration.BaseURL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(configuration.BaseURL), "/")
	}
	adapter, err := openaicompletions.New(openaicompletions.Config{
		APIKey:     configuration.APIKey,
		BaseURL:    baseURL,
		HTTPClient: configuration.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: configure Chat Completions adapter: %w", descriptor.ProviderID(), err)
	}
	descriptor.completionsAdapter = adapter
	return descriptor, nil
}

// Models returns the Zhipu models supported by this provider.
func Models() []llm.Model { return []llm.Model{DefaultModel()} }

// DefaultModel returns the GLM-5.3 coding model.
func DefaultModel() llm.Model { return model() }

// Stream validates Zhipu compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != p.ProviderID() {
		return nil, fmt.Errorf(
			"%s: model provider %q does not match %q",
			p.ProviderID(),
			request.Model.Provider,
			p.ProviderID(),
		)
	}
	if request.Model.ID != ModelGLM53 {
		return nil, fmt.Errorf("%s: unsupported model %q", p.ProviderID(), request.Model.ID)
	}
	if request.Model.API != openaicompletions.API {
		return nil, fmt.Errorf(
			"%s: model %q API %q does not match %q",
			p.ProviderID(),
			request.Model.ID,
			request.Model.API,
			openaicompletions.API,
		)
	}
	capabilities := messageCapabilities
	capabilities.ID = p.ProviderID()
	capabilities.Label = p.Label()
	if err := provider.ValidateMessages(request.Messages, capabilities); err != nil {
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
	if p.codingPlan {
		return "Zhipu Coding Plan"
	}
	return "Zhipu API"
}

// MenuDescription describes Zhipu in interactive provider menus.
func (p *Provider) MenuDescription() string {
	if p.codingPlan {
		return "GLM Coding Plan subscription (China endpoint)"
	}
	return "Zhipu API Platform (pay-as-you-go; China endpoint)"
}

// Models returns the Zhipu model catalog.
func (p *Provider) Models() []llm.Model {
	return []llm.Model{p.DefaultModel()}
}

// DefaultModel returns the Zhipu model used when none is selected.
func (p *Provider) DefaultModel() llm.Model {
	model := DefaultModel()
	model.Provider = p.ProviderID()
	return model
}

// Configured reports whether the configuration carries a Zhipu credential.
func (p *Provider) Configured(configuration config.Config) bool {
	if p.codingPlan {
		return configuration.ZhipuCodingAPIKey != ""
	}
	return configuration.ZhipuAPIKey != ""
}

// New constructs the credentialed Zhipu model service.
func (p *Provider) New(configuration config.Config) (llm.Streamer, error) {
	if p.codingPlan {
		return NewCoding(Config{
			APIKey:  configuration.ZhipuCodingAPIKey,
			BaseURL: configuration.ZhipuCodingBaseURL,
		})
	}
	return New(Config{
		APIKey:  configuration.ZhipuAPIKey,
		BaseURL: configuration.ZhipuBaseURL,
	})
}

// SaveAPIKey stores the Zhipu credential in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) {
	if p.codingPlan {
		return config.SaveZhipuCodingAPIKey(apiKey)
	}
	return config.SaveZhipuAPIKey(apiKey)
}

// ApplyAPIKey stores the Zhipu credential in the configuration.
func (p *Provider) ApplyAPIKey(configuration *config.Config, apiKey string) {
	if p.codingPlan {
		configuration.ZhipuCodingAPIKey = apiKey
		return
	}
	configuration.ZhipuAPIKey = apiKey
}

// CredentialNotConfiguredError describes the missing Zhipu credential.
func (p *Provider) CredentialNotConfiguredError() error {
	env := config.EnvZhipuAPIKey
	if p.codingPlan {
		env = config.EnvZhipuCodingAPIKey
	}
	return fmt.Errorf(
		"%s API key is not configured; run /login in interactive mode or set %s",
		p.Label(), env,
	)
}

var _ provider.Provider = (*Provider)(nil)
