package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
)

func TestAccountLoginPreservesPartialCommit(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "preference failure"} {
		t.Run(outcome, func(t *testing.T) {
			paths := authTestPaths(t)
			failure := errors.New("synthetic preference failure")
			s := newLoginTestSession(paths)
			saves := 0
			s.application.dependencies.saveSettings = func(ctx context.Context, paths config.Paths, changes map[config.Setting]string) error {
				saves++
				if outcome == "preference failure" {
					return failure
				}
				return config.SaveSettingsFile(ctx, paths, changes)
			}
			s.application.dependencies.codexInteractiveLogin = func(context.Context, bool, codex.LoginInteraction) (config.CodexCredentials, error) {
				if _, err := s.beginPreparation(); !errors.Is(err, interaction.ErrSettingsBusy) {
					t.Fatalf("login did not hold its settings reservation: %v", err)
				}
				if outcome == "cancel" {
					return config.CodexCredentials{}, context.Canceled
				}
				return config.CodexCredentials{AccessToken: "synthetic-private-token", RefreshToken: "synthetic-private-refresh", AccountID: "fixture", ExpiresAt: time.Now().Add(time.Hour)}, nil
			}
			request := interaction.CommandRequest{Name: "login", Arguments: "openai-codex", LoginMethod: "browser", Auth: &interaction.AuthInteraction{Notify: func(context.Context, interaction.AuthPrompt) error { return nil }}}
			result, err := s.RunSettingsAction(t.Context(), 0, request)
			output := result.Output
			loaded, loadErr := config.LoadFiles(paths, config.LoadOptions{})
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			switch outcome {
			case "success":
				if err != nil || !loaded.CodexCredentials.Configured() || loaded.Provider != "openai-codex" || s.configuration.Provider != "openai-codex" || s.loop == nil || saves != 1 {
					t.Fatalf("login did not publish saved preferences: %v", err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || loaded.CodexCredentials.Configured() || s.configuration.Provider != "deepseek" || s.loop != nil || saves != 0 {
					t.Fatalf("canceled login changed credential or runtime: %v", err)
				}
			case "preference failure":
				if !errors.Is(err, failure) || !strings.Contains(err.Error(), "account credential saved") || !loaded.CodexCredentials.Configured() || loaded.Provider == "openai-codex" || s.configuration.Provider != "deepseek" || s.loop != nil || saves != 1 {
					t.Fatalf("credential-only commit was lost or incorrectly published: %v", err)
				}
			}
			if _, reason := s.settingsStatus(); reason != "" {
				t.Fatalf("login leaked reservation: %s", reason)
			}
			if strings.Contains(output, "synthetic-private") {
				t.Fatal("credential exposed in output")
			}
		})
	}
}
