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
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestInteractiveAccountLogin(t *testing.T) {
	for _, method := range []string{"browser", "device-code", "cancel"} {
		t.Run(method, func(t *testing.T) {
			paths := authTestPaths(t)
			opened, notified := 0, 0
			input := make(chan string, 1)
			input <- "manual-code"
			runner := &interactiveSession{configuration: config.Config{Provider: "deepseek", Paths: paths}, model: deepseek.DefaultModel(), providers: defaultProviders(), application: &application{dependencies: dependencies{
				providers: defaultProviders(), saveSetting: func(setting config.Setting, value string) error { return config.SaveSettingFile(paths, setting, value) },
				newModel:    func(c config.Config) (llm.Streamer, error) { return &recordingModel{response: "ready"}, nil },
				openBrowser: func(context.Context, string) error { opened++; return errors.New("no browser") },
				codexInteractiveLogin: func(ctx context.Context, device bool, auth codex.LoginInteraction) (config.CodexCredentials, error) {
					if device != (method == "device-code") {
						t.Error("incorrect login method")
					}
					if err := auth.Notify(ctx, codex.LoginPrompt{URL: "https://example.test/auth", AllowInput: !device}); err != nil {
						return config.CodexCredentials{}, err
					}
					if method == "cancel" {
						return config.CodexCredentials{}, context.Canceled
					}
					if <-auth.Input != "manual-code" {
						t.Error("input not forwarded")
					}
					return config.CodexCredentials{AccessToken: "private-access", RefreshToken: "private-refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}, nil
				},
			}}}
			loginMethod := method
			if method == "cancel" {
				loginMethod = "browser"
			}
			output, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "login", Arguments: "openai-codex", LoginMethod: loginMethod, Auth: &interaction.AuthInteraction{Input: input, Notify: func(ctx context.Context, prompt interaction.AuthPrompt) error { notified++; return nil }}})
			loaded, loadErr := config.LoadFiles(paths, func(string) (string, bool) { return "", false })
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if method == "cancel" {
				if !errors.Is(err, context.Canceled) || loaded.CodexCredentials.Configured() || runner.configuration.Provider != "deepseek" {
					t.Fatal("cancel changed account state")
				}
			} else if err != nil || !loaded.CodexCredentials.Configured() || runner.loop == nil || runner.model.Provider != codex.ProviderID {
				t.Fatalf("login did not activate current Session: %v", err)
			}
			if strings.Contains(output, "private-") {
				t.Fatal("output leaked credential")
			}
			if method == "device-code" {
				if opened != 0 || notified != 1 {
					t.Fatal("device login opened browser")
				}
			} else if opened != 1 || notified != 2 {
				t.Fatal("browser fallback not delivered")
			}
		})
	}
}
