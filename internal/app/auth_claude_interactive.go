package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
)

// loginClaudeAccount runs within the existing command lifetime. Only successful
// authorization updates disk and the current Session's provider state.
func (s *interactiveSession) loginClaudeAccount(ctx context.Context, request interaction.CommandRequest) (result loginActionResult, returnErr error) {
	if request.Arguments != string(claudesubscription.ProviderID) || request.Secret != "" || request.UseSavedCredential {
		return result, errors.New("app: invalid account login request")
	}
	if request.LoginMethod != "browser" {
		return result, errors.New("app: unknown account login method")
	}
	if request.Auth == nil || request.Auth.Notify == nil {
		return result, errors.New("app: interactive authentication is required")
	}
	settings := s.settingsSnapshot()
	opened := false
	credential, err := s.application.dependencies.claudeInteractiveLogin(ctx, claudesubscription.LoginInteraction{
		Input: request.Auth.Input,
		Notify: func(ctx context.Context, prompt claudesubscription.LoginPrompt) error {
			display := interaction.AuthPrompt{Title: "Login to Claude Pro/Max", URL: prompt.URL, Code: prompt.Code,
				Instructions: prompt.Instructions, AllowInput: prompt.AllowInput}
			if err := request.Auth.Notify(ctx, display); err != nil {
				return err
			}
			if !opened && request.LoginMethod == "browser" && s.application.dependencies.openBrowser != nil {
				opened = true
				if err := s.application.dependencies.openBrowser(ctx, prompt.URL); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					display.Instructions = "Could not open the browser automatically. Open the link above, then complete login or paste the redirect URL. Escape or Ctrl+C cancels."
					return request.Auth.Notify(ctx, display)
				}
			}
			return nil
		},
	})
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	update := s.application.dependencies.updateClaudeCredentials
	if update == nil {
		update = config.UpdateClaudeSubscriptionCredentials
	}
	changed := false
	_, err = update(ctx, settings.configuration.Paths, func(previous config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
		changed = previous != credential
		return credential, nil
	})
	if err != nil && !config.WasCommitted(err) {
		return result, fmt.Errorf("app: save account login: %w", err)
	}
	// Existing OAuth providers reread this file for each request, including
	// providers retained by a side thread after the main selection changed.
	result.committed, result.resourcesChanged = changed, changed
	if err != nil {
		s.settingsWarning(fmt.Errorf("Account credential saved; %w", err))
	}
	overridden, err := s.selectProvider(ctx, string(claudesubscription.ProviderID))
	if err != nil {
		status := "unchanged"
		if result.committed {
			status = "saved"
		}
		return result, fmt.Errorf("account credential %s, but provider preferences and selection were not changed: %w", status, err)
	}
	result.committed, result.resourcesChanged = true, true
	result.output = "Signed in to Claude Pro/Max. AICE is ready.\n" + savedSettingMessage("provider", string(claudesubscription.ProviderID), overridden)
	return result, nil
}
