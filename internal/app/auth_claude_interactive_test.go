package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestInteractiveClaudeAccountLogin(t *testing.T) {
	for _, method := range []string{"browser", "cancel", "save failure"} {
		t.Run(method, func(t *testing.T) {
			paths := authTestPaths(t)
			opened, notified := 0, 0
			input := make(chan string, 1)
			input <- "manual-code"
			runner := &interactiveSession{configuration: config.Config{Provider: "deepseek", Paths: paths}, model: deepseek.DefaultModel(), providers: defaultProviders(), application: &application{dependencies: dependencies{
				providers: defaultProviders(), saveSettings: func(ctx context.Context, paths config.Paths, changes map[config.Setting]string) error {
					if method == "save failure" {
						return errors.New("synthetic preference write failure")
					}
					return config.SaveSettingsFile(ctx, paths, changes)
				},
				newModel:    func(c config.Config) (llm.Streamer, error) { return &recordingModel{response: "ready"}, nil },
				openBrowser: func(context.Context, string) error { opened++; return errors.New("no browser") },
				claudeInteractiveLogin: func(ctx context.Context, auth claudesubscription.LoginInteraction) (config.ClaudeSubscriptionCredentials, error) {
					if err := auth.Notify(ctx, claudesubscription.LoginPrompt{URL: "https://example.test/auth", AllowInput: true}); err != nil {
						return config.ClaudeSubscriptionCredentials{}, err
					}
					if method == "cancel" {
						return config.ClaudeSubscriptionCredentials{}, context.Canceled
					}
					if <-auth.Input != "manual-code" {
						t.Error("input not forwarded")
					}
					return config.ClaudeSubscriptionCredentials{AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
				},
			}}}
			loginMethod := method
			if method == "cancel" || method == "save failure" {
				loginMethod = "browser"
			}
			output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "login", Arguments: "anthropic-subscription", LoginMethod: loginMethod, Auth: &interaction.AuthInteraction{Input: input, Notify: func(ctx context.Context, prompt interaction.AuthPrompt) error { notified++; return nil }}})
			loaded, loadErr := config.LoadFiles(paths, config.LoadOptions{})
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if method == "cancel" {
				if !errors.Is(err, context.Canceled) || loaded.ClaudeSubscriptionCredentials.Configured() || runner.configuration.Provider != "deepseek" {
					t.Fatal("cancel changed account state")
				}
			} else if method == "save failure" {
				if err == nil || !strings.Contains(err.Error(), "account credential saved, but provider preferences and selection were not changed") || !loaded.ClaudeSubscriptionCredentials.Configured() || runner.configuration.Provider != "deepseek" || runner.model.Provider != deepseek.ProviderID || runner.loop != nil {
					t.Fatalf("credential-only success was not preserved: %v", err)
				}
			} else if err != nil || !loaded.ClaudeSubscriptionCredentials.Configured() || runner.loop == nil || runner.model.Provider != claudesubscription.ProviderID {
				t.Fatalf("login did not activate current Session: %v", err)
			}
			if strings.Contains(output, "private-") {
				t.Fatal("output leaked credential")
			}
			if opened != 1 || notified != 2 {
				t.Fatal("browser fallback not delivered")
			}
		})
	}
}
