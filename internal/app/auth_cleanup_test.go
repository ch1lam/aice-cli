package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestAccountLoginCleanupWarning(t *testing.T) {
	// Both entries share the coordinator; exercise each independent writer with
	// real temporary files, without repeating the held-run effect matrix.
	for _, provider := range []string{"openai-codex", "anthropic-subscription"} {
		for _, outcome := range []string{"publish", "preference failure", "unchanged"} {
			t.Run(provider+"/"+outcome, func(t *testing.T) {
				paths := authTestPaths(t)
				s := &interactiveSession{configuration: config.Config{Provider: "deepseek", Paths: paths}, model: deepseek.DefaultModel(), providers: defaultProviders()}
				failure := errors.New("synthetic preference failure")
				updates, saves := 0, 0
				s.application = &application{dependencies: dependencies{
					providers: defaultProviders(),
					newModel:  func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
					saveSettings: func(ctx context.Context, p config.Paths, changes map[config.Setting]string) error {
						saves++
						if outcome == "preference failure" {
							return failure
						}
						return config.SaveSettingsFile(ctx, p, changes)
					},
				}}
				blockCleanup := func(path string) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(path+".lock", "cleanup-blocker"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				expiry := time.Now().UTC().Add(time.Hour)
				codexCredential := config.CodexCredentials{AccessToken: "private-token", RefreshToken: "private-refresh", AccountID: "fixture", ExpiresAt: expiry}
				claudeCredential := config.ClaudeSubscriptionCredentials{AccessToken: "private-token", RefreshToken: "private-refresh", ExpiresAt: expiry}
				if outcome == "unchanged" {
					if _, err := config.UpdateCodexCredentials(t.Context(), paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return codexCredential, nil }); err != nil {
						t.Fatal(err)
					}
					if _, err := config.UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
						return claudeCredential, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				s.application.dependencies.codexInteractiveLogin = func(context.Context, bool, codex.LoginInteraction) (config.CodexCredentials, error) {
					return codexCredential, nil
				}
				s.application.dependencies.claudeInteractiveLogin = func(context.Context, claudesubscription.LoginInteraction) (config.ClaudeSubscriptionCredentials, error) {
					return claudeCredential, nil
				}
				s.application.dependencies.updateCodexCredentials = func(ctx context.Context, p config.Paths, update func(config.CodexCredentials) (config.CodexCredentials, error)) (config.CodexCredentials, error) {
					updates++
					return config.UpdateCodexCredentials(ctx, p, func(previous config.CodexCredentials) (config.CodexCredentials, error) {
						blockCleanup(config.CodexAuthPath(p))
						return update(previous)
					})
				}
				s.application.dependencies.updateClaudeCredentials = func(ctx context.Context, p config.Paths, update func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error)) (config.ClaudeSubscriptionCredentials, error) {
					updates++
					return config.UpdateClaudeSubscriptionCredentials(ctx, p, func(previous config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
						blockCleanup(config.ClaudeSubscriptionAuthPath(p))
						return update(previous)
					})
				}
				request := interaction.CommandRequest{Name: "login", Arguments: provider, LoginMethod: "browser", Auth: &interaction.AuthInteraction{Notify: func(context.Context, interaction.AuthPrompt) error { return nil }}}
				var output string
				var err error
				if provider == "openai-codex" {
					output, err = s.RunSlashCommand(t.Context(), request)
				} else {
					result, actionErr := s.RunSettingsAction(t.Context(), 0, request)
					output, err = result.Output+strings.Join(result.Warnings, "\n"), actionErr
					if result.Committed || result.Applied {
						t.Error("credential exposed as preference committed")
					}
				}
				wantRevision, wantSaves := uint64(1), 1
				if outcome == "unchanged" {
					wantRevision, wantSaves = 0, 0
					if err == nil || config.WasCommitted(err) || strings.Contains(output, "credential saved") {
						t.Errorf("unchanged cleanup failure treated as committed: %v", err)
					}
				} else {
					if !strings.Contains(output, "Account credential saved;") || !strings.Contains(output, "lock cleanup warning") {
						t.Error("credential cleanup warning missing")
					}
					if outcome == "preference failure" {
						if !errors.Is(err, failure) || !strings.Contains(err.Error(), "account credential saved") {
							t.Errorf("partial success lost: %v", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
				}
				loaded, loadErr := config.LoadFiles(paths, config.LoadOptions{})
				if loadErr != nil || provider == "openai-codex" && loaded.CodexCredentials != codexCredential || provider == "anthropic-subscription" && loaded.ClaudeSubscriptionCredentials != claudeCredential {
					t.Fatalf("credential not saved: %v", loadErr)
				}
				if outcome == "publish" {
					if loaded.Provider != provider || s.configuration.Provider != provider || s.loop == nil {
						t.Error("preference not saved/published")
					}
				} else if loaded.Provider == provider || s.configuration.Provider != "deepseek" || s.loop != nil {
					t.Error("failed preference unexpectedly published")
				}
				if updates != 1 || saves != wantSaves || s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantRevision || s.lifecycle.changing {
					t.Errorf("writes/versions/reservation=%d/%d/%d/%d/%v", updates, saves, s.lifecycle.revision, s.lifecycle.resourceRevision, s.lifecycle.changing)
				}
				if strings.Contains(output, "private-token") || strings.Contains(output, "private-refresh") {
					t.Error("credential leaked")
				}
			})
		}
	}
}
