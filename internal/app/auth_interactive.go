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
func (s *interactiveSession) loginAccount(ctx context.Context, request interaction.CommandRequest) (string, error) {
	if request.Arguments != string(codex.ProviderID) || request.Secret != "" || request.UseSavedCredential {
		return "", errors.New("app: invalid account login request")
	}
	if request.LoginMethod != "browser" && request.LoginMethod != "device-code" {
		return "", errors.New("app: unknown account login method")
	}
	if request.Auth == nil || request.Auth.Notify == nil {
		return "", errors.New("app: interactive authentication is required")
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
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_, err = config.UpdateCodexCredentials(ctx, settings.configuration.Paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return credential, nil })
	if err != nil {
		return "", fmt.Errorf("app: save account login: %w", err)
	}
	if _, err := s.slashProvider(ctx, interaction.CommandRequest{Name: "provider", Arguments: string(codex.ProviderID)}); err != nil {
		return "", err
	}
	return "Signed in to OpenAI Codex. AICE is ready.", nil
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
