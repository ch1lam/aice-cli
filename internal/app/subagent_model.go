package app

import (
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/provider"
)

// resolveSubagentModel resolves the subagents' model from subagent_model and
// subagent_thinking, or returns the main run's configured model when neither
// is set. subagent_model names a model of the main provider, or another
// provider as provider/model; an unset subagent_thinking keeps the main
// thinking setting, clamped to the subagents' model like any request.
func resolveSubagentModel(providers []provider.Provider, configured configuredModel) (configuredModel, error) {
	cfg := configured.configuration
	if cfg.SubagentModel == "" && cfg.SubagentThinking == "" {
		return configured, nil
	}
	if name := cfg.SubagentModel; name != "" {
		cfg.Model = name
		if provider, model, ok := strings.Cut(name, "/"); ok && supportedProvider(providers, provider) {
			cfg.Provider, cfg.Model = provider, model
		}
	}
	if cfg.SubagentThinking != "" {
		cfg.Thinking = cfg.SubagentThinking
	}
	model, options, err := resolveModelSettings(providers, cfg)
	if err != nil {
		return configuredModel{}, fmt.Errorf("subagent_model: %w", err)
	}
	child := configuredModel{configuration: cfg, model: model, options: options}
	if model.Provider == configured.model.Provider {
		child.service = configured.service
	} else if !providerConfigured(providers, cfg) {
		return configuredModel{}, fmt.Errorf("subagent_model: %w", credentialNotConfiguredError(providers, cfg))
	}
	return child, nil
}
