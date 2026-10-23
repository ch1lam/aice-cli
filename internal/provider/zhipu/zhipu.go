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

	ModelGLM53      = "glm-5.3"
	ModelGLM53Flash = "glm-5.3-flash"
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
	codingPlan bool
	route      provider.Route
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
	descriptor.route = provider.Route{
		Provider:                 descriptor.ProviderID(),
		Label:                    descriptor.Label(),
		Models:                   descriptor.Models(),
		Adapters:                 map[llm.API]llm.Streamer{openaicompletions.API: adapter},
		NestedToolResultTextOnly: true,
	}
	return descriptor, nil
}

// Stream validates Zhipu compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	return p.route.Stream(ctx, request)
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
	models := make([]llm.Model, 0, len(modelSpecs))
	for _, spec := range modelSpecs {
		if p.codingPlan && !codingModel(spec.id) {
			continue
		}
		models = append(models, catalogModel(spec, p.ProviderID()))
	}
	return models
}

// DefaultModel returns GLM-5.3, which both plans serve.
func (p *Provider) DefaultModel() llm.Model { return p.Models()[0] }

// credential is the selected plan's API key in AICE configuration.
func (p *Provider) credential() provider.APIKey {
	return provider.APIKey{Provider: p.ProviderID(), Label: p.Label()}
}

// Configured reports whether the configuration carries the plan's API key.
func (p *Provider) Configured(c config.Config) bool { return p.credential().Configured(c) }

// New constructs the credentialed Zhipu model service for the selected plan.
func (p *Provider) New(c config.Config) (llm.Streamer, error) {
	configuration := Config{APIKey: p.credential().Value(c), BaseURL: p.credential().BaseURL(c)}
	if p.codingPlan {
		return NewCoding(configuration)
	}
	return New(configuration)
}

// SaveAPIKey stores the plan's API key in the global auth file.
func (p *Provider) SaveAPIKey(apiKey string) (string, error) { return p.credential().Save(apiKey) }

// ApplyAPIKey stores the plan's API key in the configuration.
func (p *Provider) ApplyAPIKey(c *config.Config, apiKey string) { p.credential().Apply(c, apiKey) }

// CredentialNotConfiguredError describes the plan's missing API key.
func (p *Provider) CredentialNotConfiguredError() error { return p.credential().NotConfiguredError() }

var _ provider.Provider = (*Provider)(nil)
