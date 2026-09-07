package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
)

// Auth performs explicit OAuth operations outside model runs. Login persists
// credentials before switching settings; logout never touches Session history.
func (a *application) Auth(ctx context.Context, request cli.AuthRequest, output io.Writer) error {
	if request.Provider != string(codex.ProviderID) {
		return errors.New("app: unsupported subscription provider")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configuration, err := a.dependencies.loadConfig()
	if err != nil {
		return err
	}
	switch request.Action {
	case "login":
		credential, err := a.dependencies.codexLogin(ctx, request.DeviceCode, output)
		if err != nil {
			return err
		}
		if _, err := config.UpdateCodexCredentials(ctx, configuration.Paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return credential, nil }); err != nil {
			return err
		}
		// Save the compatible model first. Provider selection is the final step.
		model := providerModel(a.dependencies.providers, string(codex.ProviderID), configuration.Model)
		if err := config.SaveSettingFile(configuration.Paths, config.SettingModel, model.ID); err != nil {
			return err
		}
		if err := config.SaveSettingFile(configuration.Paths, config.SettingProvider, string(codex.ProviderID)); err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "Signed in to OpenAI Codex. Saved credentials to %s.\nDefault provider: openai-codex; model: %s.\n", config.CodexAuthPath(configuration.Paths), model.ID)
		return err
	case "status":
		status := "not logged in"
		if configuration.CodexCredentials.Configured() {
			status = "logged in"
			if time.Until(configuration.CodexCredentials.ExpiresAt) <= time.Minute {
				status += " (access token expires soon or has expired; refresh on next request)"
			}
		}
		_, err := fmt.Fprintln(output, "OpenAI Codex: "+status)
		return err
	case "logout":
		_, err := config.UpdateCodexCredentials(ctx, configuration.Paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return config.CodexCredentials{}, nil })
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, "Removed AICE's Codex credentials. Provider selection is unchanged.")
		return err
	default:
		return fmt.Errorf("app: unsupported auth action %q", request.Action)
	}
}
