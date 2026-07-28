// Package deepseek defines DeepSeek models and compatibility behavior.
package deepseek

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	// ProviderID is DeepSeek's provider identifier in AICE requests.
	ProviderID llm.ProviderID = "deepseek"
	// BaseURL is DeepSeek's Anthropic-compatible API root.
	BaseURL = "https://api.deepseek.com/anthropic"

	ModelV4Flash = "deepseek-v4-flash"
	ModelV4Pro   = "deepseek-v4-pro"
)

// Config contains DeepSeek connection settings.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider applies DeepSeek compatibility rules before delegating to the
// shared Anthropic Messages adapter.
type Provider struct {
	adapter *anthropic.Adapter
}

// New constructs a DeepSeek provider. An empty BaseURL selects the official
// Anthropic-compatible endpoint.
func New(config Config) (*Provider, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = BaseURL
	}

	adapter, err := anthropic.New(anthropic.Config{
		APIKey:     config.APIKey,
		BaseURL:    baseURL,
		HTTPClient: config.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseek: configure Anthropic adapter: %w", err)
	}
	return &Provider{adapter: adapter}, nil
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
	if request.Model.API != anthropic.API {
		return nil, fmt.Errorf(
			"deepseek: model API %q does not match %q",
			request.Model.API,
			anthropic.API,
		)
	}
	if request.Model.ID != ModelV4Flash && request.Model.ID != ModelV4Pro {
		return nil, fmt.Errorf("deepseek: unsupported model %q", request.Model.ID)
	}
	if err := validateMessages(request.Messages); err != nil {
		return nil, err
	}
	return p.adapter.Stream(ctx, request)
}

func model(id string) llm.Model {
	name := "DeepSeek V4 Flash"
	// Rates are USD per million tokens from DeepSeek's public model pricing.
	pricing := llm.Pricing{
		Input:     0.14,
		Output:    0.28,
		CacheRead: 0.0028,
	}
	if id == ModelV4Pro {
		name = "DeepSeek V4 Pro"
		pricing = llm.Pricing{
			Input:     0.435,
			Output:    0.87,
			CacheRead: 0.003625,
		}
	}
	return llm.Model{
		ID:               id,
		Name:             name,
		API:              anthropic.API,
		Provider:         ProviderID,
		SupportsThinking: true,
		InputModalities:  []llm.InputModality{llm.InputModalityText},
		ContextWindow:    1_000_000,
		MaxTokens:        384_000,
		Pricing:          pricing,
	}
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
							"are not supported by DeepSeek's Anthropic API",
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
		return errors.New("image content is not supported by DeepSeek's Anthropic API")
	case llm.ContentTypeThinking:
		if part.Redacted {
			return errors.New("redacted thinking is not supported by DeepSeek's Anthropic API")
		}
	case llm.ContentTypeToolResult:
		if part.ToolResult == nil {
			return nil
		}
		for _, nested := range part.ToolResult.Content {
			if nested.Type != llm.ContentTypeText {
				return errors.New("non-text tool results are not supported by DeepSeek's Anthropic API")
			}
		}
	}
	return nil
}
