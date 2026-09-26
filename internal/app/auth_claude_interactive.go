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
func (s *interactiveSession) loginClaudeAccount(ctx context.Context, request interaction.CommandRequest) (string, error) {
	if request.Arguments != string(claudesubscription.ProviderID) || request.Secret != "" || request.UseSavedCredential {
		return "", errors.New("app: invalid account login request")
	}
	if request.LoginMethod != "browser" {
		return "", errors.New("app: unknown account login method")
	}
	if request.Auth == nil || request.Auth.Notify == nil {
		return "", errors.New("app: interactive authentication is required")
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
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_, err = config.UpdateClaudeSubscriptionCredentials(ctx, settings.configuration.Paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
		return credential, nil
	})
	if err != nil {
		return "", fmt.Errorf("app: save account login: %w", err)
	}
	message, err := s.slashProvider(ctx, interaction.CommandRequest{Name: "provider", Arguments: string(claudesubscription.ProviderID)})
	if err != nil {
		return "", fmt.Errorf("account credential saved, but preferences and current Session were not changed: %w", err)
	}
	return "Signed in to Claude Pro/Max. AICE is ready.\n" + message, nil
}
