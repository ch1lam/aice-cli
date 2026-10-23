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
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestInteractiveAccountLogin(t *testing.T) {
	for _, test := range []struct {
		provider llm.ProviderID
		method   string
	}{
		{codex.ProviderID, "browser"},
		{codex.ProviderID, "device-code"},
		{codex.ProviderID, "cancel"},
		{codex.ProviderID, "save failure"},
		{claudesubscription.ProviderID, "browser"},
		{claudesubscription.ProviderID, "cancel"},
		{claudesubscription.ProviderID, "save failure"},
	} {
		t.Run(string(test.provider)+"/"+test.method, func(t *testing.T) {
			paths := authTestPaths(t)
			opened, notified := 0, 0
			input := make(chan string, 1)
			input <- "manual-code"
			// authorize drives the shared provider-neutral part of a fake login:
			// announce the URL, honor cancellation and read the manual code.
			authorize := func(ctx context.Context, device bool, notify func(context.Context, string, bool) error, codes <-chan string) error {
				if device != (test.method == "device-code") {
					t.Error("incorrect login method")
				}
				if err := notify(ctx, "https://example.test/auth", !device); err != nil {
					return err
				}
				if test.method == "cancel" {
					return context.Canceled
				}
				if <-codes != "manual-code" {
					t.Error("input not forwarded")
				}
				return nil
			}
			runner := newLoginTestSession(paths)
			dependencies := &runner.application.dependencies
			dependencies.saveSettings = func(ctx context.Context, paths config.Paths, changes map[config.Setting]string) error {
				if test.method == "save failure" {
					return errors.New("synthetic preference write failure")
				}
				return config.SaveSettingsFile(ctx, paths, changes)
			}
			dependencies.openBrowser = func(context.Context, string) error { opened++; return errors.New("no browser") }
			dependencies.codexInteractiveLogin = func(ctx context.Context, device bool, auth codex.LoginInteraction) (config.CodexCredentials, error) {
				notify := func(ctx context.Context, url string, allowInput bool) error {
					return auth.Notify(ctx, codex.LoginPrompt{URL: url, AllowInput: allowInput})
				}
				if err := authorize(ctx, device, notify, auth.Input); err != nil {
					return config.CodexCredentials{}, err
				}
				return config.CodexCredentials{AccessToken: "private-access", RefreshToken: "private-refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			dependencies.claudeInteractiveLogin = func(ctx context.Context, auth claudesubscription.LoginInteraction) (config.ClaudeSubscriptionCredentials, error) {
				notify := func(ctx context.Context, url string, allowInput bool) error {
					return auth.Notify(ctx, claudesubscription.LoginPrompt{URL: url, AllowInput: allowInput})
				}
				if err := authorize(ctx, false, notify, auth.Input); err != nil {
					return config.ClaudeSubscriptionCredentials{}, err
				}
				return config.ClaudeSubscriptionCredentials{AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			loginMethod := test.method
			if test.method == "cancel" || test.method == "save failure" {
				loginMethod = "browser"
			}
			output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "login", Arguments: string(test.provider), LoginMethod: loginMethod, Auth: &interaction.AuthInteraction{Input: input, Notify: func(ctx context.Context, prompt interaction.AuthPrompt) error { notified++; return nil }}})
			loaded, loadErr := config.LoadFiles(paths, config.LoadOptions{})
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			configured := loaded.CodexCredentials.Configured()
			if test.provider == claudesubscription.ProviderID {
				configured = loaded.ClaudeSubscriptionCredentials.Configured()
			}
			switch test.method {
			case "cancel":
				if !errors.Is(err, context.Canceled) || configured || runner.configuration.Provider != "deepseek" {
					t.Fatal("cancel changed account state")
				}
			case "save failure":
				if err == nil || !strings.Contains(err.Error(), "account credential saved, but provider preferences and selection were not changed") || !configured || runner.configuration.Provider != "deepseek" || runner.model.Provider != deepseek.ProviderID || runner.loop != nil {
					t.Fatalf("credential-only success was not preserved: %v", err)
				}
			default:
				if err != nil || !configured || runner.loop == nil || runner.model.Provider != test.provider {
					t.Fatalf("login did not activate current Session: %v", err)
				}
			}
			if strings.Contains(output, "private-") {
				t.Fatal("output leaked credential")
			}
			if test.method == "device-code" {
				if opened != 0 || notified != 1 {
					t.Fatal("device login opened browser")
				}
			} else if opened != 1 || notified != 2 {
				t.Fatal("browser fallback not delivered")
			}
		})
	}
}
