package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/claudesubscription"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestClaudeAuthCommandsLoginStatusLogout(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	if err := config.SaveOpenAIAPIKeyFile(paths, "existing-api-key"); err != nil {
		t.Fatal(err)
	}
	load := func(config.LoadOptions) (config.Config, error) {
		return config.LoadFiles(paths, config.LoadOptions{})
	}
	loginCalls := 0
	deps := dependencies{
		loadConfig: load, providers: defaultProviders(),
		newModel: func(config.Config) (llm.Streamer, error) { t.Error("auth command created model"); return nil, nil },
		claudeLogin: func(ctx context.Context, output io.Writer) (config.ClaudeSubscriptionCredentials, error) {
			loginCalls++
			return config.ClaudeSubscriptionCredentials{AccessToken: "secret-access", RefreshToken: "secret-refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	for _, action := range []string{"login", "status", "logout", "status"} {
		command, err := newCommand(deps)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"auth", action, "--provider", "anthropic-subscription"}
		command.SetArgs(args)
		var output bytes.Buffer
		command.SetOut(&output)
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if strings.Contains(output.String(), "secret-") || strings.Contains(output.String(), "existing-api-key") {
			t.Fatal("auth output leaked credential")
		}
		loaded, err := load(config.LoadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Provider != "anthropic-subscription" || loaded.Model != claudesubscription.DefaultModel().ID || loaded.OpenAIAPIKey != "existing-api-key" {
			t.Fatal("auth command did not preserve settings and other providers")
		}
		if action == "login" && !loaded.ClaudeSubscriptionCredentials.Configured() {
			t.Fatal("login did not persist credentials")
		}
		if action == "logout" && loaded.ClaudeSubscriptionCredentials.Configured() {
			t.Fatal("logout retained credentials")
		}
	}
	if loginCalls != 1 {
		t.Fatalf("login calls = %d", loginCalls)
	}
}

func TestClaudeAuthCancelledLoginLeavesSettingsAndCredentialsUnchanged(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	if err := config.SaveSettingFile(paths, config.SettingProvider, "openai"); err != nil {
		t.Fatal(err)
	}
	command, err := newCommand(dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) {
			return config.LoadFiles(paths, config.LoadOptions{})
		},
		newModel: func(config.Config) (llm.Streamer, error) { return nil, nil },
		claudeLogin: func(context.Context, io.Writer) (config.ClaudeSubscriptionCredentials, error) {
			return config.ClaudeSubscriptionCredentials{}, context.Canceled
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"auth", "login", "--provider", "anthropic-subscription"})
	if err := command.ExecuteContext(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || loaded.Provider != "openai" || loaded.ClaudeSubscriptionCredentials.Configured() {
		t.Fatalf("cancelled login altered configuration: %v", err)
	}
}

func TestClaudeSubscriptionLoginMenuSkipsAPIKeyAndReloadsSavedCredential(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	runner := &interactiveSession{
		configuration: config.Config{Provider: "deepseek", Paths: paths}, model: deepseek.DefaultModel(), providers: defaultProviders(),
		application: &application{dependencies: dependencies{
			providers: defaultProviders(), saveSettings: recordSettings(func(config.Setting, string) error { return nil }),
			newModel: func(c config.Config) (llm.Streamer, error) {
				if !c.ClaudeSubscriptionCredentials.Configured() {
					t.Error("did not load newly saved OAuth credential")
				}
				return &recordingModel{response: "ready"}, nil
			},
		}},
	}
	options := loginProviderOptions(runner.providers, runner.configuration)
	found := false
	for _, option := range options {
		if option.Arguments == "anthropic-subscription" {
			found = true
			if option.UseSavedCredential || option.Menu == nil || len(option.Menu.Options) != 1 || option.Menu.Options[0].LoginMethod != "browser" {
				t.Fatal("ClaudeSubscription menu would prompt for API key")
			}
		}
	}
	if !found {
		t.Fatal("ClaudeSubscription missing from login menu")
	}
	request := interaction.CommandRequest{Name: "login", Arguments: "anthropic-subscription", UseSavedCredential: true}
	if _, err := runner.RunSlashCommand(t.Context(), request); err == nil || !strings.Contains(err.Error(), "aice auth login") {
		t.Fatalf("missing login guidance: %v", err)
	}
	_, err := config.UpdateClaudeSubscriptionCredentials(t.Context(), paths, func(config.ClaudeSubscriptionCredentials) (config.ClaudeSubscriptionCredentials, error) {
		return config.ClaudeSubscriptionCredentials{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunSlashCommand(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if runner.model.Provider != claudesubscription.ProviderID || runner.loop == nil {
		t.Fatal("did not switch current Session to ClaudeSubscription")
	}
}

func TestClaudeAuthRejectsDeviceCodeWithoutStartingLogin(t *testing.T) {
	t.Parallel()
	command, err := newCommand(dependencies{loadConfig: func(config.LoadOptions) (config.Config, error) {
		t.Error("invalid command loaded config")
		return config.Config{}, nil
	}, newModel: func(config.Config) (llm.Streamer, error) { return nil, nil }, claudeLogin: func(context.Context, io.Writer) (config.ClaudeSubscriptionCredentials, error) {
		t.Error("device flag started login")
		return config.ClaudeSubscriptionCredentials{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"auth", "login", "--provider", "anthropic-subscription", "--device-code"})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "browser login") {
		t.Fatalf("device-code error = %v", err)
	}
}
