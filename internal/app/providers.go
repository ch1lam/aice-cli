package app

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/openai"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

// defaultProviders returns the built-in provider registry in menu order.
func defaultProviders() []provider.Provider {
	return []provider.Provider{
		&deepseek.Provider{},
		&opencode.Provider{},
		&openai.Provider{},
		&custom.Provider{},
	}
}

// findProvider returns the registered provider matching an identifier, or nil.
func findProvider(providers []provider.Provider, providerID string) provider.Provider {
	for _, candidate := range providers {
		if string(candidate.ProviderID()) == providerID {
			return candidate
		}
	}
	return nil
}

func credentialNotConfiguredError(
	providers []provider.Provider,
	configuration config.Config,
) error {
	if candidate := findProvider(providers, configuration.Provider); candidate != nil {
		return candidate.CredentialNotConfiguredError()
	}
	return fmt.Errorf(
		"API key for provider %q is not configured; run /login in "+
			"interactive mode",
		configuration.Provider,
	)
}

// knownProviders returns the provider identifiers AICE supports.
func knownProviders(providers []provider.Provider) []string {
	ids := make([]string, 0, len(providers))
	for _, candidate := range providers {
		ids = append(ids, string(candidate.ProviderID()))
	}
	return ids
}

// supportedProvider reports whether provider is one AICE can serve.
func supportedProvider(providers []provider.Provider, providerID string) bool {
	return findProvider(providers, providerID) != nil
}

// providerConfigured reports whether the selected provider has a credential.
func providerConfigured(
	providers []provider.Provider,
	configuration config.Config,
) bool {
	candidate := findProvider(providers, configuration.Provider)
	return candidate != nil && candidate.Configured(configuration)
}

// providerLabel returns the display name for a provider identifier.
func providerLabel(providers []provider.Provider, providerID string) string {
	if candidate := findProvider(providers, providerID); candidate != nil {
		return candidate.Label()
	}
	return providerID
}

// modelForConfiguration constructs the model service for the provider selected
// in the configuration.
func modelForConfiguration(
	providers []provider.Provider,
	configuration config.Config,
) (llm.Streamer, error) {
	providerID := configuration.Provider
	if providerID == "" {
		providerID = string(deepseek.ProviderID)
	}
	candidate := findProvider(providers, providerID)
	if candidate == nil {
		return nil, fmt.Errorf("app: unsupported provider %q", configuration.Provider)
	}
	return candidate.New(configuration)
}

// defaultSaveAPIKey stores a credential in the auth file of the provider it
// belongs to, preserving any other provider credentials already present.
func defaultSaveAPIKey(
	providers []provider.Provider,
	providerID, apiKey string,
) (string, error) {
	candidate := findProvider(providers, providerID)
	if candidate == nil {
		return "", fmt.Errorf("app: unsupported provider %q", providerID)
	}
	return candidate.SaveAPIKey(apiKey)
}

// modelsForProvider returns the model catalog for a provider. Unknown providers
// fall back to DeepSeek so callers that already validated the provider can rely
// on a non-empty catalog.
func modelsForProvider(providers []provider.Provider, providerID string) []llm.Model {
	if candidate := findProvider(providers, providerID); candidate != nil {
		return candidate.Models()
	}
	if deepSeek := findProvider(providers, string(deepseek.ProviderID)); deepSeek != nil {
		return deepSeek.Models()
	}
	return nil
}

// providerDefaultModel returns the default model for a provider.
func providerDefaultModel(providers []provider.Provider, providerID string) llm.Model {
	if candidate := findProvider(providers, providerID); candidate != nil {
		return candidate.DefaultModel()
	}
	if deepSeek := findProvider(providers, string(deepseek.ProviderID)); deepSeek != nil {
		return deepSeek.DefaultModel()
	}
	return llm.Model{}
}

// modelForProvider looks a model ID up in one provider's catalog.
func modelForProvider(
	providers []provider.Provider,
	providerID, id string,
) (llm.Model, bool) {
	for _, model := range modelsForProvider(providers, providerID) {
		if model.ID == id {
			return model, true
		}
	}
	if providerID == string(custom.ProviderID) && strings.TrimSpace(id) != "" {
		return custom.ModelForID(id), true
	}
	return llm.Model{}, false
}

// modelIDsForProvider returns the model IDs of one provider's catalog.
func modelIDsForProvider(providers []provider.Provider, providerID string) []string {
	models := modelsForProvider(providers, providerID)
	ids := make([]string, len(models))
	for index, model := range models {
		ids[index] = model.ID
	}
	return ids
}

func resolveModelSettings(
	providers []provider.Provider,
	configuration config.Config,
) (llm.Model, llm.StreamOptions, error) {
	switch configuration.Thinking {
	case llm.ThinkingLevelUnknown,
		llm.ThinkingLevelOff,
		llm.ThinkingLevelMinimal,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax:
	default:
		return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
			"app: unsupported thinking level %q",
			configuration.Thinking,
		)
	}

	providerID := configuration.Provider
	if providerID == "" {
		providerID = string(deepseek.ProviderID)
	}
	if !supportedProvider(providers, providerID) {
		return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			providerID,
			strings.Join(knownProviders(providers), ", "),
		)
	}

	modelID := configuration.Model
	if modelID == "" {
		modelID = providerDefaultModel(providers, providerID).ID
	}
	// The custom provider is the Pi-inspired catch-all: any model ID is
	// materialized on the fly with safe defaults (only `id` required).
	if providerID == string(custom.ProviderID) {
		model := custom.ModelForID(modelID)
		requested := configuration.Thinking
		if requested == llm.ThinkingLevelUnknown {
			requested = llm.DefaultThinkingLevel
		}
		return model, llm.StreamOptions{
			Thinking: llm.ClampThinkingLevel(model, requested),
		}, nil
	}
	for _, model := range modelsForProvider(providers, providerID) {
		if model.ID != modelID {
			continue
		}
		requested := configuration.Thinking
		if requested == llm.ThinkingLevelUnknown {
			requested = llm.DefaultThinkingLevel
		}
		return model, llm.StreamOptions{
			Thinking: llm.ClampThinkingLevel(model, requested),
		}, nil
	}
	return llm.Model{}, llm.StreamOptions{}, fmt.Errorf(
		"app: unsupported model %q for provider %q",
		modelID,
		providerID,
	)
}
