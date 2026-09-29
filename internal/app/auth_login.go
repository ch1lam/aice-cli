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

// loginActionResult records effects, including a credential committed before
// a later preference failure. OAuth updates also affect existing providers.
type loginActionResult struct {
	output           string
	committed        bool
	resourcesChanged bool
}

func (s *interactiveSession) runLoginSettings(ctx context.Context, revision *uint64, request interaction.CommandRequest) (interaction.SettingsActionResult, error) {
	if err := s.beginSettingsOperation(revision, true); err != nil {
		return interaction.SettingsActionResult{}, err
	}
	action, err := s.runLoginAction(ctx, request)
	nextRevision, warnings := s.endSettingsOperation(action.committed, action.resourcesChanged)
	return interaction.SettingsActionResult{Output: action.output, Revision: nextRevision, Warnings: warnings}, err
}

// runLoginAction applies a credential action while its caller owns the settings
// reservation. Both interactive entry points use the same login selection.
func (s *interactiveSession) runLoginAction(
	ctx context.Context,
	request interaction.CommandRequest,
) (loginActionResult, error) {
	if request.LoginMethod != "" {
		return s.loginAccount(ctx, request)
	}
	return s.login(ctx, request)
}

func (s *interactiveSession) login(
	ctx context.Context,
	request interaction.CommandRequest,
) (result loginActionResult, returnErr error) {
	if s.application == nil {
		return result, fmt.Errorf("app: application is required")
	}

	provider := strings.TrimSpace(request.Arguments)
	if provider == "" || strings.ContainsAny(provider, " \t\r\n") {
		return result, fmt.Errorf("app: select a provider through the /login menus")
	}
	settings := s.settingsSnapshot()
	if !supportedProvider(s.providers, provider) {
		return result, fmt.Errorf(
			"app: unsupported provider %q; available: %s",
			provider,
			strings.Join(knownProviders(s.providers), ", "),
		)
	}
	if provider == string(claudesubscription.ProviderID) {
		if request.Secret != "" {
			return result, fmt.Errorf("app: Claude subscriptions use OAuth; run aice auth login --provider anthropic-subscription")
		}
		overridden, err := s.selectProvider(ctx, provider)
		if err != nil {
			return result, err
		}
		return loginActionResult{output: savedSettingMessage("provider", provider, overridden), committed: true, resourcesChanged: true}, nil
	}
	if provider == string(codex.ProviderID) {
		if request.Secret != "" {
			return result, fmt.Errorf("app: Codex uses OAuth; run aice auth login --provider openai-codex")
		}
		overridden, err := s.selectProvider(ctx, provider)
		if err != nil {
			return result, err
		}
		return loginActionResult{output: savedSettingMessage("provider", provider, overridden), committed: true, resourcesChanged: true}, nil
	}

	customEndpoint := strings.TrimSpace(request.CustomEndpoint)
	customModel := strings.TrimSpace(request.CustomModel)
	if customEndpoint != "" || customModel != "" {
		if provider != string(custom.ProviderID) || request.UseSavedCredential {
			return result, fmt.Errorf("app: endpoint/model require the Custom login form")
		}
		if customEndpoint != "" && !(strings.HasPrefix(customEndpoint, "http://") || strings.HasPrefix(customEndpoint, "https://")) {
			return result, fmt.Errorf("app: custom endpoint must start with http:// or https://")
		}
	}
	apiKey := strings.TrimSpace(request.Secret)
	configuration := settings.configuration
	configuration.Provider = provider
	if request.UseSavedCredential {
		if apiKey != "" {
			return result, fmt.Errorf(
				"app: saved credential selection cannot include an API key",
			)
		}
		if !providerConfigured(s.providers, configuration) {
			return result, fmt.Errorf(
				"app: %s API key is not configured",
				providerLabel(s.providers, provider),
			)
		}
	} else {
		// The custom provider is keyless by design (Ollama); an empty key clears
		// the stored credential and still enables the provider. Other providers
		// keep the strict requirement.
		if provider != string(custom.ProviderID) && apiKey == "" {
			return result, fmt.Errorf(
				"app: %s API key is required",
				providerLabel(s.providers, provider),
			)
		}
		if strings.ContainsAny(apiKey, "\r\n") {
			return result, fmt.Errorf(
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
				return result, fmt.Errorf("app: model must not contain whitespace")
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
		return result, err
	}
	configuration.Model = model.ID
	loop, err := s.rebuildAgentLoop(configuration)
	if err != nil {
		return result, err
	}
	path := ""
	if !request.UseSavedCredential {
		path, err = s.application.dependencies.saveAPIKey(provider, apiKey)
		if err != nil && !config.WasCommitted(err) {
			return result, fmt.Errorf(
				"app: save %s API key: %w",
				providerLabel(s.providers, provider),
				err,
			)
		}
		result.committed = true
		if err != nil {
			s.settingsWarning(fmt.Errorf("API key saved; %w", err))
		}
	}

	configuration, err = s.persistSettings(ctx, configuration, changes)
	if err != nil {
		if !request.UseSavedCredential {
			if path == "" {
				return result, fmt.Errorf("credential saved, but preferences and current Session were not changed: %w", err)
			}
			return result, fmt.Errorf("credential saved to %s, but preferences and current Session were not changed: %w", path, err)
		}
		return result, err
	}
	defer func() {
		if returnErr == nil {
			result.output += savedOverrideNotice(configuration, changes)
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
	result.committed, result.resourcesChanged = true, true
	if request.UseSavedCredential {
		result.output = fmt.Sprintf(
			"Switched to %s using the saved credential. AICE is ready.",
			providerLabel(s.providers, provider),
		)
		return result, nil
	}
	location := ""
	if path != "" {
		location = " to " + path
	}
	if provider == string(custom.ProviderID) {
		endpoint := strings.TrimSpace(configuration.CustomBaseURL)
		if endpoint == "" {
			endpoint = custom.DefaultBaseURL
		}
		if apiKey == "" {
			result.output = fmt.Sprintf("Configured %s (endpoint %s, no API key). AICE is ready.", providerLabel(s.providers, provider), endpoint)
			return result, nil
		}
		result.output = fmt.Sprintf("Configured %s (endpoint %s) and saved API key%s. AICE is ready.", providerLabel(s.providers, provider), endpoint, location)
		return result, nil
	}
	result.output = "Saved " + providerLabel(s.providers, provider) + " API key" + location + ". AICE is ready."
	return result, nil
}
