package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ch1lam/aice-cli/internal/cli"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
)

// authClaude performs explicit OAuth operations outside model runs. Login persists
// credentials before switching settings; logout never touches Session history.
func (a *application) authClaude(ctx context.Context, request cli.AuthRequest, output io.Writer) error {
	if request.Provider != string(claudesubscription.ProviderID) {
		return errors.New("app: unsupported subscription provider")
	}
	if request.DeviceCode {
		return errors.New("app: Claude subscriptions use browser login")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configuration, err := a.dependencies.loadConfig(config.LoadOptions{})
	if err != nil {
		return err
	}
	switch request.Action {
	case "login":
		credential, err := a.dependencies.claudeLogin(ctx, output)
		if err != nil {
			return err
		}
		if _, err := config.UpdateClaudeSubscriptionCredentials(ctx, configuration.Paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
			return credential, nil
		}); err != nil {
			return err
		}
		// Provider and compatible model form one persisted preference change.
		model := providerModel(a.dependencies.providers, string(claudesubscription.ProviderID), configuration.Model)
		if err := config.SaveSettingsFile(ctx, configuration.Paths, map[config.Setting]string{config.SettingModel: model.ID, config.SettingProvider: string(claudesubscription.ProviderID)}); err != nil {
			return fmt.Errorf("credentials saved, but defaults were not changed: %w", err)
		}

		_, err = fmt.Fprintf(output, "Signed in to Claude Pro/Max. Saved credentials to %s.\nDefault provider: anthropic-subscription; model: %s.\n", config.ClaudeSubscriptionAuthPath(configuration.Paths), model.ID)
		return err
	case "status":
		status := "not logged in"
		if configuration.ClaudeSubscriptionCredentials.Configured() {
			status = "logged in"
			if time.Until(configuration.ClaudeSubscriptionCredentials.ExpiresAt) <= 5*time.Minute {
				status += " (access token expires soon or has expired; refresh on next request)"
			}
		}
		_, err := fmt.Fprintln(output, "Claude Pro/Max: "+status)
		return err
	case "logout":
		_, err := config.UpdateClaudeSubscriptionCredentials(ctx, configuration.Paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
			return config.ClaudeSubscriptionCredentials{}, nil
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, "Removed AICE's Claude subscription credentials. Provider selection is unchanged.")
		return err
	default:
		return fmt.Errorf("app: unsupported auth action %q", request.Action)
	}
}
