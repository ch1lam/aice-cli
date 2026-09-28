package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
)

// Model selection operations prepare, save, and publish under the caller's
// settings reservation. They do not acquire a second reservation. The returned
// flag reports whether startup sources override any preference saved here.
func (s *interactiveSession) selectProvider(ctx context.Context, value string) (overridden bool, err error) {
	if !supportedProvider(s.providers, value) {
		return false, fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			value,
			strings.Join(knownProviders(s.providers), ", "),
		)
	}
	settings := s.settingsSnapshot()
	configuration := settings.configuration
	configuration.Provider = value
	if value == string(codex.ProviderID) {
		configuration.CodexCredentials, err = config.LoadCodexCredentials(configuration.Paths)
		if err != nil {
			return false, err
		}
	}
	if value == string(claudesubscription.ProviderID) {
		configuration.ClaudeSubscriptionCredentials, err = config.LoadClaudeSubscriptionCredentials(configuration.Paths)
		if err != nil {
			return false, err
		}
	}
	model := providerModel(s.providers, value, configuration.Model)
	changes := map[config.Setting]string{config.SettingProvider: value}
	if model.ID != configuration.Model {
		changes[config.SettingModel] = model.ID
	}
	configuration.Model = model.ID
	loop, err := s.rebuildAgentLoop(configuration)
	if err != nil {
		return false, err
	}
	configuration, err = s.persistSettings(ctx, configuration, changes)
	if err != nil {
		return false, err
	}
	configuration.Model = model.ID
	effective := clampedThinkingForModel(model, configuration.Thinking)
	// The settings transition is one critical section so a concurrent side
	// snapshot freezes a mutually consistent provider/model/thinking tuple.
	s.stateMu.Lock()
	s.configuration = configuration
	s.modelErr = nil
	s.loop = loop
	s.model = applyContextWindow(model, configuration)
	s.options.Thinking = effective
	s.stateMu.Unlock()
	return configuration.SavedValuesOverridden(changes), nil
}

func (s *interactiveSession) selectModel(ctx context.Context, value string) (overridden bool, err error) {
	settings := s.settingsSnapshot()
	providerID := activeProvider(settings.model, settings.configuration)
	model, exists := modelForProvider(s.providers, providerID, value)
	if !exists {
		return false, fmt.Errorf(
			"app: unsupported model %q; available: %s",
			value,
			strings.Join(modelIDsForProvider(s.providers, providerID), ", "),
		)
	}
	effective := clampedThinkingForModel(
		model,
		settings.configuration.Thinking,
	)
	configuration := settings.configuration
	configuration.Model = value
	loop := settings.loop
	if settings.modelErr != nil && providerConfigured(s.providers, configuration) {
		loop, err = s.rebuildAgentLoop(configuration)
		if err != nil {
			return false, err
		}
	}
	changes := map[config.Setting]string{config.SettingModel: value}
	configuration, err = s.persistSettings(ctx, configuration, changes)
	if err != nil {
		return false, err
	}
	// The settings transition is one critical section so a concurrent side
	// snapshot freezes a mutually consistent model/thinking pair.
	s.stateMu.Lock()
	s.configuration = configuration
	s.modelErr = nil
	s.loop = loop
	s.model = applyContextWindow(model, settings.configuration)
	s.options.Thinking = effective
	s.stateMu.Unlock()
	return configuration.SavedValuesOverridden(changes), nil
}

func (s *interactiveSession) selectThinking(ctx context.Context, value string) (overridden bool, err error) {
	level := llm.ThinkingLevel(value)
	settings := s.settingsSnapshot()
	_, options, err := resolveModelSettings(s.providers, config.Config{
		Provider: settings.configuration.Provider,
		Model:    settings.model.ID,
		Thinking: level,
	})
	if err != nil {
		return false, err
	}
	changes := map[config.Setting]string{config.SettingThinking: value}
	configuration, err := s.persistSettings(ctx, settings.configuration, changes)
	if err != nil {
		return false, err
	}
	s.stateMu.Lock()
	s.configuration = configuration
	s.options.Thinking = options.Thinking
	s.stateMu.Unlock()
	return configuration.SavedValuesOverridden(changes), nil
}
