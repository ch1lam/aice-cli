package app

import (
	"context"
	"fmt"
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
// or creating a Session. It never probes the native desktop; that status is
// read separately. All credential rows contain status and actions only.
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
		status, tone := "not configured", interaction.ToneMuted
		if candidate != nil && candidate.Configured(settings.configuration) {
			status, tone = "configured", interaction.ToneGood
		}
		source := settings.configuration.CredentialSource(option.Arguments)
		switch option.Arguments {
		case "openai-codex":
			source = config.Source{Kind: "OAuth store", Location: config.CodexAuthPath(settings.configuration.Paths)}
		case "anthropic-subscription":
			source = config.Source{Kind: "OAuth store", Location: config.ClaudeSubscriptionAuthPath(settings.configuration.Paths)}
		}
		details := []interaction.Detail{{Key: "Credential", Value: status, Tone: tone}, {Key: "Source", Value: strings.TrimSpace(source.Kind + " " + source.Location), Tone: interaction.ToneReference}}
		menu := &interaction.CommandMenu{Title: option.Label + " account", Options: []interaction.CommandOption{option}}
		result.Fields = append(result.Fields, interaction.SettingField{ID: "account." + option.Arguments, Category: "models", Label: option.Label + " account", Description: interaction.DetailsText(details), Details: details, Value: interaction.SettingValue{Text: status}, Tone: tone, Kind: interaction.SettingAction, Applies: interaction.SettingDomainAction, DisabledReason: disabled, Action: &interaction.Command{Name: "login", SecretPrompt: "API key", Menu: menu}})
	}
	trust := interaction.Command{Name: "trust", Menu: s.trustMenu()}
	trustReason := ""
	if disabled == interaction.ErrSettingsBusy.Error() {
		trustReason = disabled
	}
	result.Fields = append(result.Fields, interaction.SettingField{ID: "project.trust", Category: "project", Label: "Change project trust", Kind: interaction.SettingAction, Applies: interaction.SettingDomainAction, Action: &trust, DisabledReason: trustReason})
	// Tools & Network sections follow this order: MCP, the presets built on
	// it, then web search with its sources row beside the search switch.
	mcpServices := mcpManagementStatus(settings.configuration, settings.mcp, "")
	s.applyMCPLoadedTools(mcpServices)
	result.Fields = append(result.Fields, mcpSettingFields(settings.configuration, mcpServices, trustReason)...)
	result.Fields = append(result.Fields, s.browserSettingFields(settings.configuration, settings.mcp, disabled)...)
	result.Fields = append(result.Fields, s.desktopSettingField(settings.configuration, settings.mcp, disabled))
	webFields := webSettingFields(settings.configuration, disabled)
	result.Fields = append(result.Fields, webFields[0], s.webSourcesField(disabled))
	result.Fields = append(result.Fields, webFields[1:]...)
	skillDetails := skillsDetails(s.skills, s.skillDiags, s.workspacePath)
	for _, entry := range []struct {
		id, category, label, value string
		details                    []interaction.Detail
	}{
		{"project.directory", "project", "Working directory", s.workspacePath, nil},
		{"project.loaded", "project", "Loaded project trust", fmt.Sprintf("%v (%v)", s.trustDecision, s.trustSource), nil},
		{"project.skills", "project", "Skills", interaction.DetailsText(skillDetails), skillDetails},
		{"system.settings_path", "system", "User settings", settings.configuration.Paths.GlobalSettings, nil},
		{"system.project_path", "system", "Project settings (read only)", settings.configuration.Paths.ProjectSettings, nil},
		{"system.auth_path", "system", "Credentials", settings.configuration.Paths.GlobalAuth, nil},
	} {
		result.Fields = append(result.Fields, interaction.SettingField{ID: entry.id, Category: entry.category, Label: entry.label, Kind: interaction.SettingInfo, Description: entry.value, Details: entry.details})
	}
	for i, diagnostic := range result.Diagnostics {
		result.Fields = append(result.Fields, interaction.SettingField{ID: fmt.Sprintf("system.diagnostic.%d", i), Category: "system", Kind: interaction.SettingInfo, Label: "Configuration diagnostic", Description: diagnostic})
	}
	if err := ctx.Err(); err != nil {
		return interaction.SettingsSnapshot{}, err
	}
	result.Runtime = s.settingsDisplay(settings)
	for i := range result.Fields {
		result.Fields[i].Section = settingSection(result.Fields[i])
	}
	return result, nil
}

// settingsDisplay carries display values without consuming SessionChanged/Transcript.
func (s *interactiveSession) settingsDisplay(settings interactiveSettings) interaction.RuntimeState {
	history, _ := s.conversation.sideSnapshot()
	return interaction.RuntimeState{Model: interaction.DisplayModel{ID: settings.model.ID}, Thinking: interaction.DisplayThinking(settings.options.Thinking), APIKeyConfigured: providerConfigured(s.providers, settings.configuration), Context: contextDisplay(settings.model, settings.configuration, settings.systemPrompt, settings.tools, history)}
}

// settingSection groups fields for display by their application-owned IDs.
// Sections are presentation, not settings or persistence boundaries.
func settingSection(field interaction.SettingField) string {
	id := field.ID
	switch field.Category {
	case "models":
		switch {
		case strings.HasPrefix(id, "account."):
			return "Accounts"
		case strings.HasSuffix(id, "_base_url"):
			return "Provider endpoints"
		default:
			return "Model & reasoning"
		}
	case "tools":
		switch {
		case strings.HasPrefix(id, "mcp."):
			return "MCP services"
		case strings.HasPrefix(id, "browser"):
			return "Browser"
		case strings.HasPrefix(id, "desktop"):
			return "Computer Use"
		case strings.HasPrefix(id, "web.service."):
			instance := strings.TrimPrefix(id, "web.service.")
			if end := strings.LastIndexByte(instance, '.'); end >= 0 {
				return "Search service · " + instance[:end]
			}
		case strings.HasPrefix(id, "web.fetch."):
			return "Web fetch"
		case strings.HasPrefix(id, "web."):
			return "Web search"
		}
	case "limits":
		switch id {
		case "run_no_progress_limit":
			return "Progress detection"
		default:
			return "Run budgets"
		}
	case "project":
		switch id {
		case "default_project_trust", "project.trust", "project.loaded":
			return "Trust"
		default:
			return "Workspace"
		}
	case "system":
		switch {
		case strings.HasPrefix(id, "system.diagnostic."):
			return "Diagnostics"
		case strings.HasPrefix(id, "system."):
			return "Configuration files"
		default:
			return "Startup"
		}
	}
	return "General"
}
