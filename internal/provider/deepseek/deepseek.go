// Package deepseek defines DeepSeek models and compatibility behavior.
package deepseek

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	// ProviderID is DeepSeek's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "deepseek"
	// BaseURL is DeepSeek's OpenAI-compatible API root.
	BaseURL = "https://api.deepseek.com"
	// AnthropicBaseURL is DeepSeek's Anthropic-compatible API root.
	AnthropicBaseURL = BaseURL + "/anthropic"

	ModelV4Flash = "deepseek-v4-flash"
	ModelV4Pro   = "deepseek-v4-pro"
)

// Config contains DeepSeek connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider applies DeepSeek compatibility rules before delegating to the wire
// protocol selected by the request model.
type Provider struct {
	anthropicAdapter *anthropic.Adapter
	responsesAdapter *openairesponses.Adapter
}

// New constructs a DeepSeek provider. An empty BaseURL selects the official
// protocol endpoints. A custom Anthropic endpoint ending in /anthropic is also
// used to derive the sibling Responses endpoint.
func New(config Config) (*Provider, error) {
	anthropicBaseURL, responsesBaseURL := protocolBaseURLs(config.BaseURL)

	anthropicAdapter, err := anthropic.New(anthropic.Config{
		APIKey:     config.APIKey,
		BaseURL:    anthropicBaseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseek: configure Anthropic adapter: %w", err)
	}
	responsesAdapter, err := openairesponses.New(openairesponses.Config{
		APIKey:     config.APIKey,
		BaseURL:    responsesBaseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseek: configure Responses adapter: %w", err)
	}
	return &Provider{
		anthropicAdapter: anthropicAdapter,
		responsesAdapter: responsesAdapter,
	}, nil
}

// Models returns the DeepSeek models supported by this provider.
func Models() []llm.Model {
	return []llm.Model{model(ModelV4Flash), model(ModelV4Pro)}
}

// DefaultModel returns the fast model used when no explicit model is selected.
func DefaultModel() llm.Model {
	return model(ModelV4Flash)
}

// Stream validates provider-specific compatibility before making a request.
func (p *Provider) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if request.Model.Provider != ProviderID {
		return nil, fmt.Errorf(
			"deepseek: model provider %q does not match %q",
			request.Model.Provider,
			ProviderID,
		)
	}
	if request.Model.ID != ModelV4Flash && request.Model.ID != ModelV4Pro {
		return nil, fmt.Errorf("deepseek: unsupported model %q", request.Model.ID)
	}
	expectedAPI := model(request.Model.ID).API
	if request.Model.API != expectedAPI {
		return nil, fmt.Errorf(
			"deepseek: model %q API %q does not match %q",
			request.Model.ID,
			request.Model.API,
			expectedAPI,
		)
	}
	if err := validateMessages(request.Messages); err != nil {
		return nil, err
	}
	switch request.Model.API {
	case anthropic.API:
		return p.anthropicAdapter.Stream(ctx, request)
	case openairesponses.API:
		return p.responsesAdapter.Stream(ctx, request)
	default:
		return nil, fmt.Errorf(
			"deepseek: model %q uses unsupported API %q",
			request.Model.ID,
			request.Model.API,
		)
	}
}

func model(id string) llm.Model {
	name := "DeepSeek V4 Flash"
	api := openairesponses.API
	// Rates are USD per million tokens from DeepSeek's public model pricing.
	pricing := llm.Pricing{
		Input:     0.14,
		Output:    0.28,
		CacheRead: 0.0028,
	}
	if id == ModelV4Pro {
		name = "DeepSeek V4 Pro"
		api = anthropic.API
		pricing = llm.Pricing{
			Input:     0.435,
			Output:    0.87,
			CacheRead: 0.003625,
		}
	}
	return llm.Model{
		ID:               id,
		Name:             name,
		API:              api,
		Provider:         ProviderID,
		SupportsThinking: true,
		InputModalities:  []llm.InputModality{llm.InputModalityText},
		ContextWindow:    1_000_000,
		MaxTokens:        384_000,
		Pricing:          pricing,
	}
}

func protocolBaseURLs(configured string) (string, string) {
	configured = strings.TrimRight(strings.TrimSpace(configured), "/")
	if configured == "" {
		return AnthropicBaseURL, BaseURL
	}

	responsesBaseURL := configured
	if strings.HasSuffix(responsesBaseURL, "/anthropic") {
		responsesBaseURL = strings.TrimSuffix(responsesBaseURL, "/anthropic")
		return configured, responsesBaseURL
	}
	return configured + "/anthropic", responsesBaseURL
}

func validateMessages(messages []llm.Message) error {
	for messageIndex, message := range messages {
		var content []llm.ContentPart
		switch value := message.(type) {
		case llm.UserMessage:
			content = value.Content
		case llm.AssistantMessage:
			content = value.Content
		case llm.ToolResultMessage:
			for partIndex, part := range value.Content {
				if part.Type != llm.ContentTypeText {
					return fmt.Errorf(
						"deepseek: message %d content %d: non-text tool results "+
							"are not supported by DeepSeek models",
						messageIndex,
						partIndex,
					)
				}
			}
			continue
		case nil:
			return fmt.Errorf("deepseek: message %d is nil", messageIndex)
		default:
			return fmt.Errorf(
				"deepseek: message %d has unsupported type %T",
				messageIndex,
				message,
			)
		}
		for partIndex, part := range content {
			if err := validateContent(part); err != nil {
				return fmt.Errorf(
					"deepseek: message %d content %d: %w",
					messageIndex,
					partIndex,
					err,
				)
			}
		}
	}
	return nil
}

func validateContent(part llm.ContentPart) error {
	switch part.Type {
	case llm.ContentTypeImage:
		return errors.New("image content is not supported by DeepSeek models")
	case llm.ContentTypeThinking:
		if part.Redacted {
			return errors.New("redacted thinking is not supported by DeepSeek models")
		}
	case llm.ContentTypeToolResult:
		if part.ToolResult == nil {
			return nil
		}
		for _, nested := range part.ToolResult.Content {
			if nested.Type != llm.ContentTypeText {
				return errors.New("non-text tool results are not supported by DeepSeek models")
			}
		}
	}
	return nil
}
