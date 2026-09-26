package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
)

var _ interaction.SettingsReader = (*interactiveSession)(nil)
var _ interaction.SettingsWriter = (*interactiveSession)(nil)

func settingsCategories() []interaction.SettingCategory {
	return []interaction.SettingCategory{
		{ID: "models", Label: "Models & Accounts"},
		{ID: "tools", Label: "Tools & Network"},
		{ID: "limits", Label: "Run Limits"},
		{ID: "project", Label: "Project & Trust"},
		{ID: "system", Label: "System"},
	}
}

func settingPresentation(id string) (category, label, description string, timing interaction.SettingTiming, invert bool) {
	timing = interaction.SettingNextRun
	switch id {
	case "provider":
		return "models", "Provider", "Model service used for the next response.", timing, false
	case "model":
		return "models", "Model", "Select from this provider's catalog, or enter a custom model ID.", timing, false
	case "thinking":
		return "models", "Thinking", "Requested reasoning level; the model may use its nearest supported level.", timing, false
	case "context_windows":
		return "models", "Context windows", "Override a provider/model capacity in tokens. Removing an entry uses its catalog default.", timing, false
	case "browser_headed":
		return "tools", "Show browser window", "Changes visibility of the next managed browser session.", interaction.SettingNextBrowser, false
	case "desktop_enabled":
		return "tools", "Computer Use", "Allow desktop access beyond the project. Task window content may be sent to the current model provider; actions affect real applications. User preference only; enabled does not mean ready.", timing, false
	case "desktop_control_mode":
		return "tools", "Computer Use control mode", "Background only refuses actions that need foreground input. Foreground allowed may affect your real keyboard, pointer and focused window. Applies to the next run.", timing, false
	case "max_turns":
		return "limits", "Maximum turns", "0 means unlimited model turns.", timing, false
	case "run_token_budget":
		return "limits", "Token budget", "0 means unlimited. Applies to the next agent run, including its follow-ups.", timing, false
	case "run_timeout":
		return "limits", "Run timeout", "0s means unlimited. Examples: 30m, 1h30m.", timing, false
	case "run_no_progress_limit":
		return "limits", "Repeated tool limit", "0 disables detection; otherwise use at least 2 consecutive repeated calls.", timing, false
	case "default_project_trust":
		return "project", "Default project trust", "Startup policy for projects without a saved decision; does not reload this project.", interaction.SettingRestart, false
	case "no_dep_install":
		return "system", "Allow helper downloads", "Allow missing verified host helpers to be installed on startup.", interaction.SettingRestart, true
	case "no_update_check":
		return "system", "Check for updates", "Check for AICE updates on startup.", interaction.SettingRestart, true
	default:
		if name, ok := strings.CutSuffix(id, "_base_url"); ok {
			return "models", name + " endpoint", "Empty uses the provider default endpoint. Canceling the user override restores inherited configuration.", timing, false
		}
		return "system", id, "", timing, false
	}
}

func publicSettingValue(value config.SettingValue) interaction.SettingValue {
	result := interaction.SettingValue{Kind: interaction.SettingKind(value.Kind), Text: value.Text, Bool: value.Bool, Int: value.Int, Duration: value.Duration}
	for _, entry := range value.Windows {
		result.Contexts = append(result.Contexts, interaction.ContextWindowSetting{Provider: entry.Provider, Model: entry.Model, Tokens: entry.Tokens})
	}
	return result
}

func publicSettingPointer(value *config.SettingValue) *interaction.SettingValue {
	if value == nil {
		return nil
	}
	result := publicSettingValue(*value)
	return &result
}

// ReadSettings projects immutable descriptions without consuming RuntimeState
// or creating a Session. All credential rows contain status and actions only.
func (s *interactiveSession) ReadSettings(ctx context.Context) (interaction.SettingsSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return interaction.SettingsSnapshot{}, err
	}
	s.lifecycle.mu.Lock()
	settings := s.settingsSnapshot()
	revision, disabled := s.lifecycle.statusLocked()
	s.lifecycle.mu.Unlock()
	result := interaction.SettingsSnapshot{Revision: revision, Categories: settingsCategories(), SavePath: settings.configuration.Paths.GlobalSettings, Diagnostics: append([]string(nil), settings.configuration.Diagnostics...)}
	for _, def := range config.SettingDefinitions() {
		state, err := settings.configuration.SettingState(def.ID)
		if err != nil {
			return interaction.SettingsSnapshot{}, err
		}
		category, label, description, timing, invert := settingPresentation(string(def.ID))
		field := interaction.SettingField{
			ID: string(def.ID), Category: category, Label: label, Description: description, Kind: interaction.SettingKind(def.Kind),
			Value: publicSettingValue(state.Value), Saved: publicSettingPointer(state.Saved), Inherited: publicSettingPointer(state.Inherited), Default: publicSettingPointer(&def.Default),
			Source: interaction.SettingSource{Kind: state.Source.Kind, Location: state.Source.Location}, InheritanceError: state.InheritanceError, Applies: timing, InvertBool: invert,
		}
		if timing != interaction.SettingRestart {
			field.DisabledReason = disabled
		}
		switch def.ID {
		case config.SettingDesktopEnabled, config.SettingDesktopControlMode:
			field.Keywords = []string{"desktop", "computer", "cua", "permission", "background", "foreground"}
			if runtime.GOOS != "darwin" {
				field.DisabledReason = "Computer Use native setup is not yet integrated on this platform"
			} else if s.desktop == nil {
				field.DisabledReason = "Computer Use runtime is unavailable in this session"
			}
			if def.ID == config.SettingDesktopEnabled {
				field.Action = &interaction.Command{Name: "desktop", Interactive: true}
				field.Arguments = "setup"
			}
			if def.ID == config.SettingDesktopControlMode {
				field.Choices = []interaction.SettingChoice{{Value: string(config.DesktopBackgroundOnly), Label: "Background only"}, {Value: string(config.DesktopForegroundAllowed), Label: "Foreground allowed"}}
			}
		case config.SettingProvider:
			field.Effective = activeProvider(settings.model, settings.configuration)
			for _, candidate := range s.providers {
				field.Choices = append(field.Choices, interaction.SettingChoice{Value: string(candidate.ProviderID()), Label: candidate.Label(), Description: candidate.MenuDescription()})
			}
		case config.SettingModel:
			field.Effective = settings.model.ID
			providerID := activeProvider(settings.model, settings.configuration)
			field.AllowCustom = providerID == string(custom.ProviderID)
			for _, model := range modelsForProvider(s.providers, providerID) {
				field.Choices = append(field.Choices, interaction.SettingChoice{Value: model.ID, Label: model.Name, Description: model.ID})
			}
			if settings.modelErr != nil {
				field.Description += " Current model unavailable: " + settings.modelErr.Error()
			}
		case config.SettingThinking:
			field.Effective = string(settings.options.Thinking)
			for _, level := range llm.SupportedThinkingLevels(settings.model) {
				label, description := thinkingLevelDescription(level)
				field.Choices = append(field.Choices, interaction.SettingChoice{Value: string(level), Label: label, Description: description})
			}
		case "default_project_trust":
			field.Choices = []interaction.SettingChoice{{Value: "ask", Label: "Ask"}, {Value: "always", Label: "Always trust"}, {Value: "never", Label: "Never trust"}}
		}
		if def.ID == config.SettingProvider || def.ID == config.SettingModel {
			field.ResetIDs = []string{"provider", "model", "thinking"}
			if def.ID == config.SettingProvider {
				field.DefaultChanges = []interaction.SettingChange{{ID: "provider", Value: interaction.SettingValue{Kind: interaction.SettingEnum}}, {ID: "model", Value: interaction.SettingValue{Kind: interaction.SettingEnum}}}
			}
			reset := config.SettingsPatch{Changes: []config.SettingChange{{ID: config.SettingProvider, Unset: true}, {ID: config.SettingModel, Unset: true}, {ID: config.SettingThinking, Unset: true}}}
			inherited, err := settings.configuration.WithPatch(reset)
			if err == nil {
				model, options, modelErr := resolveModelSettings(s.providers, inherited)
				err = modelErr
				if err == nil {
					field.Description += fmt.Sprintf(" Reset group (provider, model, thinking) inherits %s / %s / %s.", model.Provider, model.ID, options.Thinking)
				}
			}
			if err != nil {
				field.Inherited = nil
				field.InheritanceError = err.Error()
			}
		}
		if def.ID == "context_windows" {
			catalog, _ := modelForProvider(s.providers, string(settings.model.Provider), settings.model.ID)
			field.Description += fmt.Sprintf(" Current catalog capacity: %d tokens; effective capacity: %d tokens.", catalog.ContextWindow, settings.model.ContextWindow)
			for _, provider := range s.providers {
				for _, model := range modelsForProvider(s.providers, string(provider.ProviderID())) {
					field.Choices = append(field.Choices, interaction.SettingChoice{Value: string(model.Provider) + "/" + model.ID, Label: fmt.Sprintf("%s/%s: %d", model.Provider, model.ID, model.ContextWindow)})
				}
			}
		}
		result.Fields = append(result.Fields, field)
	}
	for _, option := range loginProviderOptions(s.providers, settings.configuration) {
		candidate := findProvider(s.providers, option.Arguments)
		status := "Not configured"
		if candidate != nil && candidate.Configured(settings.configuration) {
			status = "Credential configured"
		}
		key := strings.ReplaceAll(option.Arguments, "-", "_") + "_api_key"
		switch option.Arguments {
		case "kimi-coding":
			key = "kimi_api_key"
		case "opencode-go":
			key = "opencode_api_key"
		}
		source := settings.configuration.CredentialSource(key)
		switch option.Arguments {
		case "openai-codex":
			source = config.Source{Kind: "OAuth store", Location: config.CodexAuthPath(settings.configuration.Paths)}
		case "anthropic-subscription":
			source = config.Source{Kind: "OAuth store", Location: config.ClaudeSubscriptionAuthPath(settings.configuration.Paths)}
		}
		status += " · " + source.Kind + " " + source.Location
		menu := &interaction.CommandMenu{Title: option.Label + " account", Options: []interaction.CommandOption{option}}
		result.Fields = append(result.Fields, interaction.SettingField{ID: "account." + option.Arguments, Category: "models", Label: option.Label + " account", Description: status, Kind: interaction.SettingAction, Applies: interaction.SettingDomainAction, DisabledReason: disabled, Action: &interaction.Command{Name: "login", SecretPrompt: "API key", Menu: menu}})
	}
	commands := []interaction.Command{
		{Name: "browser", Menu: s.browserMenu(), Interactive: true},
		{Name: "web", Menu: s.webMenu(), Interactive: true},
		{Name: "trust", Menu: s.trustMenu()},
	}
	for _, entry := range []struct{ id, category, label, name string }{{"browser.actions", "tools", "Browser connection and tabs", "browser"}, {"web.services", "tools", "Search services and priority", "web"}, {"project.trust", "project", "Change project trust", "trust"}} {
		for _, command := range commands {
			if command.Name == entry.name {
				reason := disabled
				if entry.name == "trust" && reason != interaction.ErrSettingsBusy.Error() {
					reason = ""
				}
				if entry.name == "browser" {
					if runtime.GOOS == "windows" {
						reason = "Browser automation is unavailable on Windows"
					} else if s.browser == nil {
						reason = "Browser manager is unavailable in this session"
					}
				}
				result.Fields = append(result.Fields, interaction.SettingField{ID: entry.id, Category: entry.category, Label: entry.label, Kind: interaction.SettingAction, Applies: interaction.SettingDomainAction, Action: &command, DisabledReason: reason})
				break
			}
		}
	}
	browserState := "Browser manager unavailable"
	if s.browser != nil {
		target := s.browser.Target()
		browserState = fmt.Sprintf("Session: %s\nManaged window visible: %t\nAuto-detect: %t\nEndpoint: %s\nUse browser actions for live connection and tab status.", s.browser.Name(), s.browser.Headed(), target.Auto, target.Endpoint)
	}
	result.Fields = append(result.Fields, interaction.SettingField{ID: "browser.status", Category: "tools", Label: "Browser status", Kind: interaction.SettingInfo, Description: browserState}, interaction.SettingField{ID: "web.status", Category: "tools", Label: "Web status", Kind: interaction.SettingInfo, Description: s.webStatus()})
	for _, entry := range []struct{ id, category, label, value string }{
		{"project.directory", "project", "Working directory", s.workspacePath},
		{"project.loaded", "project", "Loaded project trust", fmt.Sprintf("%v (%v)", s.trustDecision, s.trustSource)},
		{"project.skills", "project", "Skills", formatSkillsCommand(s.skills, s.skillDiags, s.workspacePath)},
		{"system.settings_path", "system", "User settings", settings.configuration.Paths.GlobalSettings},
		{"system.project_path", "system", "Project settings (read only)", settings.configuration.Paths.ProjectSettings},
		{"system.auth_path", "system", "Credentials", settings.configuration.Paths.GlobalAuth},
	} {
		result.Fields = append(result.Fields, interaction.SettingField{ID: entry.id, Category: entry.category, Label: entry.label, Kind: interaction.SettingInfo, Description: entry.value})
	}
	for i, diagnostic := range result.Diagnostics {
		result.Fields = append(result.Fields, interaction.SettingField{ID: fmt.Sprintf("system.diagnostic.%d", i), Category: "system", Kind: interaction.SettingInfo, Label: "Configuration diagnostic", Description: diagnostic})
	}
	result.Fields = append(result.Fields, webSettingFields(settings.configuration, disabled)...)
	desktopReason := disabled
	if runtime.GOOS != "darwin" {
		desktopReason = "Computer Use native setup is not yet integrated on this platform"
	} else if s.desktop == nil {
		desktopReason = "Computer Use runtime is unavailable in this session"
	}
	result.Fields = append(result.Fields, interaction.SettingField{ID: "desktop.setup", Category: "tools", Label: "Computer Use setup / repair", Description: "Install the signed Driver and request OS permissions, or retry saving the enabled preference. No Session is created.", Kind: interaction.SettingAction, Action: desktopSetupCommand(), Applies: interaction.SettingDomainAction, DisabledReason: desktopReason})
	result.Fields = append(result.Fields, s.desktopStatusField(ctx, settings))
	if err := ctx.Err(); err != nil {
		return interaction.SettingsSnapshot{}, err
	}
	result.Runtime = s.settingsDisplay(settings)
	return result, nil
}

// settingsDisplay carries display values without consuming SessionChanged/Transcript.
func (s *interactiveSession) settingsDisplay(settings interactiveSettings) interaction.RuntimeState {
	history, _ := s.conversation.sideSnapshot()
	return interaction.RuntimeState{Model: interaction.DisplayModel{ID: settings.model.ID}, Thinking: interaction.DisplayThinking(settings.options.Thinking), APIKeyConfigured: providerConfigured(s.providers, settings.configuration), Context: contextDisplay(settings.model, settings.configuration, settings.systemPrompt, settings.tools, history)}
}
