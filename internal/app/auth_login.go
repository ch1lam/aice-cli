package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
)

// runLoginAction applies a credential action while its caller owns the settings
// reservation. Both interactive entry points use the same login selection.
func (s *interactiveSession) runLoginAction(
	ctx context.Context,
	request interaction.CommandRequest,
) (string, error) {
	if request.LoginMethod != "" {
		return s.loginAccount(ctx, request)
	}
	return s.login(ctx, request)
}

func (s *interactiveSession) login(
	ctx context.Context,
	request interaction.CommandRequest,
) (message string, returnErr error) {
	if s.application == nil {
		return "", fmt.Errorf("app: application is required")
	}

	provider := strings.TrimSpace(request.Arguments)
	if provider == "" || strings.ContainsAny(provider, " \t\r\n") {
		return "", fmt.Errorf("app: select a provider through the /login menus")
	}
	settings := s.settingsSnapshot()
	if !supportedProvider(s.providers, provider) {
		return "", fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			provider,
			strings.Join(knownProviders(s.providers), ", "),
		)
	}
	if provider == string(claudesubscription.ProviderID) {
		if request.Secret != "" {
			return "", fmt.Errorf("app: Claude subscriptions use OAuth; run aice auth login --provider anthropic-subscription")
		}
		overridden, err := s.selectProvider(ctx, provider)
		if err != nil {
			return "", err
		}
		return savedSettingMessage("provider", provider, overridden), nil
	}
	if provider == string(codex.ProviderID) {
		if request.Secret != "" {
			return "", fmt.Errorf("app: Codex uses OAuth; run aice auth login --provider openai-codex")
		}
		overridden, err := s.selectProvider(ctx, provider)
		if err != nil {
			return "", err
		}
		return savedSettingMessage("provider", provider, overridden), nil
	}

	customEndpoint := strings.TrimSpace(request.CustomEndpoint)
	customModel := strings.TrimSpace(request.CustomModel)
	if customEndpoint != "" || customModel != "" {
		if provider != string(custom.ProviderID) || request.UseSavedCredential {
			return "", fmt.Errorf("app: endpoint/model require the Custom login form")
		}
		if customEndpoint != "" && !(strings.HasPrefix(customEndpoint, "http://") || strings.HasPrefix(customEndpoint, "https://")) {
			return "", fmt.Errorf("app: custom endpoint must start with http:// or https://")
		}
	}
	apiKey := strings.TrimSpace(request.Secret)
	configuration := settings.configuration
	configuration.Provider = provider
	if request.UseSavedCredential {
		if apiKey != "" {
			return "", fmt.Errorf(
				"app: saved credential selection cannot include an API key",
			)
		}
		if !providerConfigured(s.providers, configuration) {
			return "", fmt.Errorf(
				"app: %s API key is not configured",
				providerLabel(s.providers, provider),
			)
		}
	} else {
		// The custom provider is keyless by design (Ollama); an empty key clears
		// the stored credential and still enables the provider. Other providers
		// keep the strict requirement.
		if provider != string(custom.ProviderID) && apiKey == "" {
			return "", fmt.Errorf(
				"app: %s API key is required",
				providerLabel(s.providers, provider),
			)
		}
		if strings.ContainsAny(apiKey, "\r\n") {
			return "", fmt.Errorf(
				"app: %s API key must be one line",
				providerLabel(s.providers, provider),
			)
		}
	}
	// Prepare and validate the whole preference change before writing it.
	if provider == string(custom.ProviderID) {
		if customEndpoint != "" {
			configuration.CustomBaseURL = customEndpoint
		}
		if customModel != "" {
			if strings.ContainsAny(customModel, " \t\r\n") {
				return "", fmt.Errorf("app: model must not contain whitespace")
			}
			configuration.Model = customModel
		}
	}
	if !request.UseSavedCredential {
		findProvider(s.providers, provider).ApplyAPIKey(&configuration, apiKey)
	}
	model := providerModel(s.providers, provider, configuration.Model)
	changes := map[config.Setting]string{config.SettingProvider: provider}
	if model.ID != settings.configuration.Model {
		changes[config.SettingModel] = model.ID
	}
	if customEndpoint != "" {
		changes[config.SettingCustomBaseURL] = customEndpoint
	}
	configuration, err := configuration.WithSettings(changes)
	if err != nil {
		return "", err
	}
	configuration.Model = model.ID
	loop, err := s.rebuildAgentLoop(configuration)
	if err != nil {
		return "", err
	}
	path := ""
	if !request.UseSavedCredential {
		path, err = s.application.dependencies.saveAPIKey(provider, apiKey)
		if err != nil {
			return "", fmt.Errorf(
				"app: save %s API key: %w",
				providerLabel(s.providers, provider),
				err,
			)
		}
	}

	configuration, err = s.persistSettings(ctx, configuration, changes)
	if err != nil {
		if !request.UseSavedCredential {
			return "", fmt.Errorf("credential saved to %s, but preferences and current Session were not changed: %w", path, err)
		}
		return "", err
	}
	defer func() {
		if returnErr == nil {
			message += savedOverrideNotice(configuration, changes)
		}
	}()
	effective := clampedThinkingForModel(model, configuration.Thinking)
	s.stateMu.Lock()
	s.configuration = configuration
	s.modelErr = nil
	s.loop = loop
	s.model = applyContextWindow(model, configuration)
	s.options.Thinking = effective
	s.stateMu.Unlock()
	if request.UseSavedCredential {
		return fmt.Sprintf(
			"Switched to %s using the saved credential. AICE is ready.",
			providerLabel(s.providers, provider),
		), nil
	}
	if provider == string(custom.ProviderID) {
		endpoint := strings.TrimSpace(configuration.CustomBaseURL)
		if endpoint == "" {
			endpoint = custom.DefaultBaseURL
		}
		if apiKey == "" {
			return fmt.Sprintf("Configured %s (endpoint %s, no API key). AICE is ready.", providerLabel(s.providers, provider), endpoint), nil
		}
		return fmt.Sprintf("Configured %s (endpoint %s) and saved API key to %s. AICE is ready.", providerLabel(s.providers, provider), endpoint, path), nil
	}
	return "Saved " + providerLabel(s.providers, provider) + " API key to " + path +
		". AICE is ready.", nil
}
