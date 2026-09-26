package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func privateSettingsPatch(request interaction.SettingsRequest) (config.SettingsPatch, error) {
	patch := config.SettingsPatch{}
	for _, change := range request.Changes {
		value := change.Value
		if change.Unset && (value.Kind != "" || value.Text != "" || value.Bool || value.Int != 0 || value.Duration != 0 || value.Contexts != nil || value.List != nil) {
			return patch, fmt.Errorf("unset cannot carry a value")
		}
		if !change.Unset {
			if err := value.Validate(); err != nil {
				return patch, err
			}
		}
		if value.List != nil {
			return patch, fmt.Errorf("app: scalar preference cannot contain a list")
		}
		private := config.SettingValue{Kind: config.ValueKind(value.Kind), Text: value.Text, Bool: value.Bool, Int: value.Int, Duration: value.Duration}
		for _, entry := range value.Contexts {
			private.Windows = append(private.Windows, config.ContextWindow{Provider: entry.Provider, Model: entry.Model, Tokens: entry.Tokens})
		}
		patch.Changes = append(patch.Changes, config.SettingChange{ID: config.Setting(change.ID), Unset: change.Unset, Value: private})
	}
	return patch, nil
}

func (s *interactiveSession) ApplySettings(ctx context.Context, request interaction.SettingsRequest) (result interaction.SettingsResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(request.Changes) == 0 {
		return result, fmt.Errorf("app: setting change is required")
	}

	shared := false
	timing := interaction.SettingRestart
	for _, change := range request.Changes {
		_, _, _, applies, _ := settingPresentation(change.ID)
		if applies != interaction.SettingRestart {
			shared = true
			timing = applies
		}
	}
	if err := s.beginSettingsOperation(&request.Revision, shared); err != nil {
		return result, err
	}
	defer func() {
		if returnErr != nil {
			result.FieldErrors = map[string]string{}
			for _, change := range request.Changes {
				result.FieldErrors[change.ID] = returnErr.Error()
			}
		}
		var warnings []string
		result.Revision, warnings = s.endSettingsOperation(result.Committed)
		result.Warnings = append(result.Warnings, warnings...)
	}()
	result.Applies = timing
	if strings.HasPrefix(request.Changes[0].ID, "web.") {
		return s.applyWebRequest(ctx, request)
	}
	patch, err := privateSettingsPatch(request)
	if err != nil {
		return result, err
	}
	// Model commands and the panel use the same domain operation. Their
	// menus are presentation only; current candidates are revalidated here.
	if len(request.Changes) == 1 && !request.Changes[0].Unset {
		change := request.Changes[0]
		if (change.ID == "provider" || change.ID == "model" || change.ID == "thinking") && change.Value.Text != "" {
			if change.Value.Kind != interaction.SettingEnum || change.Value.Bool || change.Value.Int != 0 || change.Value.Duration != 0 || len(change.Value.Contexts) != 0 {
				return result, fmt.Errorf("app: invalid model selection value")
			}
			handler := slashCommandHandlers[change.ID]
			_, err := handler(s, ctx, interaction.CommandRequest{Name: change.ID, Arguments: change.Value.Text})
			result.Committed, result.Applied = err == nil, err == nil
			if err == nil {
				c := s.settingsSnapshot().configuration
				if c.SavedValuesOverridden(map[config.Setting]string{config.Setting(change.ID): change.Value.Text}) {
					result.Warnings = append(result.Warnings, "Flag, environment or project settings may override this preference on the next startup")
				}
			}
			return result, err
		}
	}
	current := s.settingsSnapshot()
	candidate, err := current.configuration.WithPatch(patch)
	if err != nil {
		return result, err
	}
	model, options, modelErr := resolveModelSettings(s.providers, candidate)
	changesModel := false
	for _, change := range request.Changes {
		if change.ID == "provider" || change.ID == "model" || change.ID == "thinking" || change.ID == "context_windows" || strings.HasSuffix(change.ID, "_base_url") {
			changesModel = true
		}
	}
	if modelErr != nil && changesModel {
		return result, modelErr
	}
	loop := current.loop
	if shared && !providerConfigured(s.providers, candidate) {
		loop = nil
	}
	if shared && modelErr == nil && s.application != nil && providerConfigured(s.providers, candidate) {
		loop, err = s.rebuildAgentLoop(candidate)
		if err != nil {
			return result, err
		}
	}
	commit, err := config.SaveSettingsPatch(ctx, candidate.Paths, patch)
	result.Committed = commit.Committed
	if err != nil {
		return result, err
	}
	if commit.CleanupWarning != nil {
		result.Warnings = append(result.Warnings, "Saved, but lock cleanup failed: "+commit.CleanupWarning.Error())
	}
	s.stateMu.Lock()
	s.configuration = candidate
	if shared {
		s.loop = loop
		if modelErr == nil {
			s.model = applyContextWindow(model, candidate)
			s.options = options
			s.modelErr = nil
		}
	}
	s.stateMu.Unlock()
	if shared {
		result.Applied = true
	}
	for _, change := range request.Changes {
		if change.ID == "browser_headed" && s.browser != nil {
			s.browser.SetHeaded(candidate.BrowserHeaded)
			if err := applyBrowserEnvironment(s.browser); err != nil {
				result.Warnings = append(result.Warnings, "Preference saved; browser environment: "+err.Error())
			}
		}
	}
	return result, nil
}

func (s *interactiveSession) RunSettingsAction(ctx context.Context, revision uint64, request interaction.CommandRequest) (string, error) {
	switch request.Name {
	case "login", "web", "browser", "trust":
	default:
		return "", fmt.Errorf("unsupported settings action %s", request.Name)
	}
	if err := s.beginSettingsOperation(&revision, request.Name != "trust"); err != nil {
		return "", err
	}
	// Actions may save credentials before a later preference write fails. Any
	// completed action invalidates older drafts, including partial success.
	output, err := slashCommandHandlers[request.Name](s, ctx, request)
	changed, _ := slashChangesResources(request)
	_, warnings := s.endSettingsOperation(changed)
	if len(warnings) > 0 {
		output += "\n" + strings.Join(warnings, "\n")
	}
	return output, err
}
