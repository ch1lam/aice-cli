package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
)

// loginAccount runs within the existing command lifetime. Only successful
// authorization updates disk and the current Session's provider state.
func (s *interactiveSession) loginAccount(ctx context.Context, request interaction.CommandRequest) (result loginActionResult, returnErr error) {
	if request.Arguments == "anthropic-subscription" {
		return s.loginClaudeAccount(ctx, request)
	}
	if request.Arguments != string(codex.ProviderID) || request.Secret != "" || request.UseSavedCredential {
		return result, errors.New("app: invalid account login request")
	}
	if request.LoginMethod != "browser" && request.LoginMethod != "device-code" {
		return result, errors.New("app: unknown account login method")
	}
	if request.Auth == nil || request.Auth.Notify == nil {
		return result, errors.New("app: interactive authentication is required")
	}
	settings := s.settingsSnapshot()
	opened := false
	credential, err := s.application.dependencies.codexInteractiveLogin(ctx, request.LoginMethod == "device-code", codex.LoginInteraction{
		Input: request.Auth.Input,
		Notify: func(ctx context.Context, prompt codex.LoginPrompt) error {
			display := interaction.AuthPrompt{Title: "Login to OpenAI Codex", URL: prompt.URL, Code: prompt.Code,
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
	update := s.application.dependencies.updateCodexCredentials
	if update == nil {
		update = config.UpdateCodexCredentials
	}
	changed := false
	_, err = update(ctx, settings.configuration.Paths, func(previous config.CodexCredentials) (config.CodexCredentials, error) {
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
	overridden, err := s.selectProvider(ctx, string(codex.ProviderID))
	if err != nil {
		status := "unchanged"
		if result.committed {
			status = "saved"
		}
		return result, fmt.Errorf("account credential %s, but provider preferences and selection were not changed: %w", status, err)
	}
	result.committed, result.resourcesChanged = true, true
	result.output = "Signed in to OpenAI Codex. AICE is ready.\n" + savedSettingMessage("provider", string(codex.ProviderID), overridden)
	return result, nil
}

func openBrowser(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", address)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address)
	default:
		command = exec.CommandContext(ctx, "xdg-open", address)
	}
	return command.Run()
}
