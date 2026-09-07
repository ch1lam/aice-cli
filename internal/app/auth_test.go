package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func authTestPaths(t *testing.T) config.Paths {
	t.Helper()
	dir := t.TempDir()
	return config.Paths{GlobalAuth: filepath.Join(dir, "auth.json"), GlobalSettings: filepath.Join(dir, "settings.json"), GlobalTrust: filepath.Join(dir, "trust.json"), BinDir: filepath.Join(dir, "bin")}
}

func TestAuthCommandsLoginStatusLogout(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	if err := config.SaveOpenAIAPIKeyFile(paths, "existing-api-key"); err != nil {
		t.Fatal(err)
	}
	load := func() (config.Config, error) {
		return config.LoadFiles(paths, func(string) (string, bool) { return "", false })
	}
	loginCalls := 0
	deps := dependencies{
		loadConfig: load, providers: defaultProviders(),
		newModel: func(config.Config) (llm.Streamer, error) { t.Error("auth command created model"); return nil, nil },
		codexLogin: func(ctx context.Context, device bool, output io.Writer) (config.CodexCredentials, error) {
			loginCalls++
			if !device {
				t.Error("device flag not forwarded")
			}
			return config.CodexCredentials{AccessToken: "secret-access", RefreshToken: "secret-refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	for _, action := range []string{"login", "status", "logout", "status"} {
		command, err := newCommand(deps)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"auth", action, "--provider", "openai-codex"}
		if action == "login" {
			args = append(args, "--device-code")
		}
		command.SetArgs(args)
		var output bytes.Buffer
		command.SetOut(&output)
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if strings.Contains(output.String(), "secret-") || strings.Contains(output.String(), "existing-api-key") {
			t.Fatal("auth output leaked credential")
		}
		loaded, err := load()
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Provider != "openai-codex" || loaded.Model != codex.DefaultModel().ID || loaded.OpenAIAPIKey != "existing-api-key" {
			t.Fatal("auth command did not preserve settings and other providers")
		}
		if action == "login" && !loaded.CodexCredentials.Configured() {
			t.Fatal("login did not persist credentials")
		}
		if action == "logout" && loaded.CodexCredentials.Configured() {
			t.Fatal("logout retained credentials")
		}
	}
	if loginCalls != 1 {
		t.Fatalf("login calls = %d", loginCalls)
	}
}

func TestAuthCancelledLoginLeavesSettingsAndCredentialsUnchanged(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	if err := config.SaveSettingFile(paths, config.SettingProvider, "openai"); err != nil {
		t.Fatal(err)
	}
	command, err := newCommand(dependencies{
		loadConfig: func() (config.Config, error) {
			return config.LoadFiles(paths, func(string) (string, bool) { return "", false })
		},
		newModel: func(config.Config) (llm.Streamer, error) { return nil, nil },
		codexLogin: func(context.Context, bool, io.Writer) (config.CodexCredentials, error) {
			return config.CodexCredentials{}, context.Canceled
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"auth", "login"})
	if err := command.ExecuteContext(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	loaded, err := config.LoadFiles(paths, func(string) (string, bool) { return "", false })
	if err != nil || loaded.Provider != "openai" || loaded.CodexCredentials.Configured() {
		t.Fatalf("cancelled login altered configuration: %v", err)
	}
}

func TestCodexLoginMenuSkipsAPIKeyAndReloadsSavedCredential(t *testing.T) {
	t.Parallel()
	paths := authTestPaths(t)
	runner := &interactiveSession{
		configuration: config.Config{Provider: "deepseek", Paths: paths}, model: deepseek.DefaultModel(), providers: defaultProviders(),
		application: &application{dependencies: dependencies{
			providers: defaultProviders(), saveSetting: func(config.Setting, string) error { return nil },
			newModel: func(c config.Config) (llm.Streamer, error) {
				if !c.CodexCredentials.Configured() {
					t.Error("did not load newly saved OAuth credential")
				}
				return &recordingModel{response: "ready"}, nil
			},
		}},
	}
	options := loginProviderOptions(runner.providers, runner.configuration)
	found := false
	for _, option := range options {
		if option.Arguments == "openai-codex" {
			found = true
			if option.UseSavedCredential || option.Menu == nil || len(option.Menu.Options) != 2 || option.Menu.Options[0].LoginMethod != "browser" || option.Menu.Options[1].LoginMethod != "device-code" {
				t.Fatal("Codex menu would prompt for API key")
			}
		}
	}
	if !found {
		t.Fatal("Codex missing from login menu")
	}
	request := interaction.CommandRequest{Name: "login", Arguments: "openai-codex", UseSavedCredential: true}
	if _, err := runner.RunSlashCommand(t.Context(), request); err == nil || !strings.Contains(err.Error(), "aice auth login") {
		t.Fatalf("missing login guidance: %v", err)
	}
	_, err := config.UpdateCodexCredentials(t.Context(), paths, func(config.CodexCredentials) (config.CodexCredentials, error) {
		return config.CodexCredentials{AccessToken: "access", RefreshToken: "refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunSlashCommand(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if runner.model.Provider != codex.ProviderID || runner.loop == nil {
		t.Fatal("did not switch current Session to Codex")
	}
}
