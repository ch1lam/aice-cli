package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
)

func TestLoginEffectsKeepOrInvalidateHeldRuns(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		for _, scenario := range []struct {
			name, provider                                                   string
			credentialFailure, preferenceFailure, warning, unchanged, cancel bool
			wantRevision, wantResources                                      uint64
		}{
			{name: "key save failure", provider: "deepseek", credentialFailure: true},
			{name: "key only", provider: "deepseek", preferenceFailure: true, wantRevision: 1},
			{name: "key cleanup warning then preference failure", provider: "deepseek", warning: true, preferenceFailure: true, wantRevision: 1},
			{name: "key cleanup warning then publish", provider: "deepseek", warning: true, wantRevision: 1, wantResources: 1},
			{name: "Codex credential only", provider: "openai-codex", preferenceFailure: true, wantRevision: 1, wantResources: 1},
			{name: "Codex unchanged then preference failure", provider: "openai-codex", preferenceFailure: true, unchanged: true},
			{name: "Claude credential only", provider: "anthropic-subscription", preferenceFailure: true, wantRevision: 1, wantResources: 1},
			{name: "Claude unchanged then preference failure", provider: "anthropic-subscription", preferenceFailure: true, unchanged: true},
			{name: "Codex canceled", provider: "openai-codex", cancel: true},
		} {
			t.Run(entry+"/"+scenario.name, func(t *testing.T) {
				h := newSideHarness(t, func() (agent.Model, error) { return &recordingModel{response: "answer"}, nil })
				s := h.session
				paths := authTestPaths(t)
				s.configuration.Paths = paths
				if err := config.SaveDeepSeekAPIKeyFile(paths, s.configuration.DeepSeekAPIKey); err != nil {
					t.Fatal(err)
				}
				expiry := time.Now().UTC().Add(time.Hour)
				oldCodex := config.CodexCredentials{AccessToken: "old-token", RefreshToken: "refresh", AccountID: "fixture", ExpiresAt: expiry}
				oldClaude := config.ClaudeSubscriptionCredentials{AccessToken: "old-token", RefreshToken: "refresh", ExpiresAt: expiry}
				if _, err := config.UpdateCodexCredentials(t.Context(), paths, func(config.CodexCredentials) (config.CodexCredentials, error) { return oldCodex, nil }); err != nil {
					t.Fatal(err)
				}
				if _, err := config.UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
					return oldClaude, nil
				}); err != nil {
					t.Fatal(err)
				}
				type modelRecord struct {
					model *recordingModel
					key   string
				}
				var records []modelRecord
				s.application.dependencies.newModel = func(c config.Config) (llm.Streamer, error) {
					model := &recordingModel{response: "answer"}
					records = append(records, modelRecord{model, c.DeepSeekAPIKey})
					return model, nil
				}
				var err error
				s.loop, err = s.rebuildAgentLoop(s.configuration)
				if err != nil {
					t.Fatal(err)
				}
				main, err := s.NewRun(t.Context(), interaction.RunInput{Prompt: "held main"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, side, err := s.CreateSideThread("held side")
				if err != nil {
					t.Fatal(err)
				}
				failure := errors.New("synthetic save failure")
				cleanup := errors.New("synthetic cleanup warning")
				keySaves, preferenceSaves := 0, 0
				s.application.dependencies.saveAPIKey = func(_ string, key string) (string, error) {
					keySaves++
					if scenario.credentialFailure {
						return "", failure
					}
					if err := config.SaveDeepSeekAPIKeyFile(paths, key); err != nil {
						return "", err
					}
					if scenario.warning {
						return "", &config.CommittedError{Warning: cleanup}
					}
					return paths.GlobalAuth, nil
				}
				s.application.dependencies.saveSettings = func(ctx context.Context, p config.Paths, changes map[config.Setting]string) error {
					preferenceSaves++
					if scenario.preferenceFailure {
						return failure
					}
					return config.SaveSettingsFile(ctx, p, changes)
				}
				s.application.dependencies.codexInteractiveLogin = func(context.Context, bool, codex.LoginInteraction) (config.CodexCredentials, error) {
					if scenario.cancel {
						return config.CodexCredentials{}, context.Canceled
					}
					credential := oldCodex
					if !scenario.unchanged {
						credential.AccessToken = "new-private-token"
					}
					return credential, nil
				}
				s.application.dependencies.claudeInteractiveLogin = func(context.Context, claudesubscription.LoginInteraction) (config.ClaudeSubscriptionCredentials, error) {
					credential := oldClaude
					if !scenario.unchanged {
						credential.AccessToken = "new-private-token"
					}
					return credential, nil
				}
				request := interaction.CommandRequest{Name: "login", Arguments: scenario.provider, Secret: "new-private-key"}
				if scenario.provider != "deepseek" {
					request.Secret, request.LoginMethod = "", "browser"
					request.Auth = &interaction.AuthInteraction{Notify: func(context.Context, interaction.AuthPrompt) error { return nil }}
				}
				var output string
				if entry == "slash" {
					output, err = s.RunSlashCommand(t.Context(), request)
				} else {
					result, actionErr := s.RunSettingsAction(t.Context(), 0, request)
					output, err = result.Output+strings.Join(result.Warnings, "\n"), actionErr
					if result.Committed || result.Applied || result.Revision != scenario.wantRevision {
						t.Errorf("unexpected Settings result: %+v", result)
					}
				}
				if scenario.cancel {
					if !errors.Is(err, context.Canceled) {
						t.Errorf("cancel result: %v", err)
					}
				} else if scenario.credentialFailure || scenario.preferenceFailure {
					if !errors.Is(err, failure) {
						t.Errorf("save failure result: %v", err)
					}
				} else if err != nil {
					t.Errorf("login result: %v", err)
				}
				if scenario.warning && !strings.Contains(output, cleanup.Error()) {
					t.Error("lost cleanup warning")
				}
				if strings.Contains(output, "new-private") {
					t.Error("output leaked credential")
				}
				if scenario.preferenceFailure && scenario.provider != "deepseek" && !scenario.unchanged && (err == nil || !strings.Contains(err.Error(), "provider preferences and selection were not changed")) {
					t.Errorf("ambiguous OAuth partial success: %v", err)
				}
				if scenario.preferenceFailure && scenario.provider != "deepseek" && (strings.Contains(err.Error(), "current Session unchanged") || scenario.unchanged && !strings.Contains(err.Error(), "account credential unchanged")) {
					t.Errorf("incorrect OAuth failure state: %v", err)
				}
				if s.lifecycle.revision != scenario.wantRevision || s.lifecycle.resourceRevision != scenario.wantResources || s.lifecycle.changing {
					t.Errorf("revision/reservation=%d/%d/%v; want %d/%d/false", s.lifecycle.revision, s.lifecycle.resourceRevision, s.lifecycle.changing, scenario.wantRevision, scenario.wantResources)
				}
				loaded, err := config.LoadFiles(paths, config.LoadOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if scenario.provider == "deepseek" {
					wantKey := "new-private-key"
					if scenario.credentialFailure {
						wantKey = "test-key"
					}
					if loaded.DeepSeekAPIKey != wantKey || keySaves != 1 {
						t.Error("credential write lost or replayed")
					}
				} else if scenario.provider == "openai-codex" {
					wantToken := "new-private-token"
					if scenario.unchanged || scenario.cancel {
						wantToken = "old-token"
					}
					if loaded.CodexCredentials.AccessToken != wantToken {
						t.Error("Codex disk value incorrect")
					}
				} else {
					wantToken := "new-private-token"
					if scenario.unchanged {
						wantToken = "old-token"
					}
					if loaded.ClaudeSubscriptionCredentials.AccessToken != wantToken {
						t.Error("Claude disk value incorrect")
					}
				}
				wantSaves := 1
				if scenario.credentialFailure || scenario.cancel {
					wantSaves = 0
				}
				if preferenceSaves != wantSaves {
					t.Errorf("preference saves=%d want %d", preferenceSaves, wantSaves)
				}
				mainErr, sideErr := main.Run(t.Context()), runSide(t, side, "held side")
				invalidated := scenario.wantResources != 0
				if invalidated {
					if !errors.Is(mainErr, interaction.ErrSettingsStale) || !errors.Is(sideErr, interaction.ErrSettingsStale) || s.SideThreads()[0].Status != interaction.SideThreadReadOnly {
						t.Errorf("held runs accepted after resource change: %v/%v", mainErr, sideErr)
					}
				} else if mainErr != nil || sideErr != nil {
					t.Errorf("held runs invalidated without resource change: %v/%v", mainErr, sideErr)
				}
				calls := 0
				for _, record := range records {
					calls += len(record.model.requests)
					if len(record.model.requests) > 0 && record.key != "test-key" {
						t.Error("retained model consumed new API credential")
					}
				}
				snapshot, err := h.store.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if invalidated && (calls != 0 || len(snapshot.Messages) != 0) || !invalidated && (calls != 2 || len(snapshot.Messages) != 2) {
					t.Errorf("model calls=%d durable messages=%d invalidated=%v", calls, len(snapshot.Messages), invalidated)
				}
			})
		}
	}
}
