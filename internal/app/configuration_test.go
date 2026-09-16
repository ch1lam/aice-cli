package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
	"github.com/ch1lam/aice-cli/internal/update"
)

func writeConfigFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectConfigRequiresTrustAndCannotAuthorizeItself(t *testing.T) {
	t.Parallel()
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "untrusted", true: "approved"}[approved], func(t *testing.T) {
			paths := authTestPaths(t)
			workspace := t.TempDir()
			paths.ProjectSettings = filepath.Join(workspace, ".aice", "settings.json")
			writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"user-model","custom_base_url":"https://user.example","no_dep_install":true}`)
			writeConfigFixture(t, paths.ProjectSettings, `{"model":"project-model","custom_base_url":"https://project.example","default_project_trust":"always"}`)
			var received config.Config
			command, err := newTestCommand(t, dependencies{
				loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
				newModel:   func(c config.Config) (llm.Streamer, error) { received = c; return &recordingModel{response: "ok"}, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--workspace", workspace, "--print", "test"}
			if approved {
				args = append(args, "--approve")
			}
			command.SetArgs(args)
			command.SetOut(io.Discard)
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := "user-model"
			if approved {
				want = "project-model"
			}
			if received.Model != want {
				t.Fatalf("model = %q, want %q", received.Model, want)
			}
		})
	}
}

func TestCommandFlagsAndOperationalConfiguration(t *testing.T) {
	for _, env := range config.EnvironmentVariables() {
		t.Setenv(env, "")
	}
	t.Setenv(config.EnvModel, "environment-model")
	paths := authTestPaths(t)
	workspace := t.TempDir()
	paths.ProjectSettings = filepath.Join(workspace, ".aice", "settings.json")
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":123,"no_dep_install":true,"no_update_check":true}`)
	writeConfigFixture(t, paths.ProjectSettings, `{"model":"project-model"}`)
	var helperChecked, tuiChecked bool
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) {
			options.Environment = true
			return config.LoadFiles(paths, options)
		},
		ensureHelpers: func(_ context.Context, options deps.Options) error {
			helperChecked = true
			if options.NoInstall {
				t.Error("explicit false flag did not beat file true")
			}
			return nil
		},
		newModel: func(c config.Config) (llm.Streamer, error) {
			if c.Model != "flag-model" {
				t.Errorf("model = %q", c.Model)
			}
			return &recordingModel{}, nil
		},
		checkUpdate: func(context.Context) (update.StartupResult, error) {
			t.Error("disabled update check ran")
			return update.StartupResult{}, nil
		},
		runTUI: func(ctx context.Context, _ interaction.Runner, options tui.Options) error {
			tuiChecked = true
			if options.Model.ID != "flag-model" {
				t.Errorf("TUI model = %q", options.Model.ID)
			}
			result, err := options.CheckUpdate(ctx)
			if err != nil || result.Status != tui.UpdateCheckStatusDisabled {
				t.Errorf("update status = %v: %v", result.Status, err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", workspace, "--approve", "--model", "flag-model", "--no-dep-install=false"})
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !helperChecked || !tuiChecked {
		t.Fatal("startup consumers were not exercised")
	}
}

func TestCustomLoginPersistsOnePreferenceBatchAndReportsFailure(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "write failure"}[fail], func(t *testing.T) {
			paths := authTestPaths(t)
			writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"old","custom_base_url":"https://old.example"}`)
			initial, err := config.LoadFiles(paths, config.LoadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			model, options, err := resolveModelSettings(defaultProviders(), initial)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			runner := &interactiveSession{configuration: initial, model: model, options: options, providers: defaultProviders(), application: &application{dependencies: dependencies{
				providers: defaultProviders(),
				newModel: func(c config.Config) (llm.Streamer, error) {
					if c.Model != "Org/Model.v1" || c.CustomBaseURL != "https://new.example" {
						t.Error("constructed service before selection was prepared")
					}
					return &recordingModel{}, nil
				},
				saveAPIKey: func(_, key string) (string, error) { return paths.GlobalAuth, config.SaveCustomAPIKeyFile(paths, key) },
				saveSettings: func(ctx context.Context, p config.Paths, changes map[config.Setting]string) error {
					calls++
					if len(changes) != 3 || changes[config.SettingModel] != "Org/Model.v1" || changes[config.SettingCustomBaseURL] != "https://new.example" {
						t.Errorf("incomplete batch: %#v", changes)
					}
					if fail {
						return errors.New("disk unavailable")
					}
					return config.SaveSettingsFile(ctx, p, changes)
				},
			}}}
			message, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "login", Arguments: "custom https://new.example Org/Model.v1", Secret: "test-private-key"})
			if calls != 1 {
				t.Fatalf("save calls = %d: %v", calls, err)
			}
			disk, loadErr := config.LoadFiles(paths, config.LoadOptions{})
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if fail {
				if err == nil || !strings.Contains(err.Error(), "credential saved") || disk.Model != "old" || runner.configuration.Model != "old" || disk.CustomBaseURL != "https://old.example" {
					t.Fatalf("partial change or misleading failure: %v", err)
				}
			} else if err != nil || disk.Model != "Org/Model.v1" || runner.configuration.Model != "Org/Model.v1" {
				t.Fatalf("selection not saved: %v", err)
			}
			if strings.Contains(message, "test-private-key") {
				t.Fatal("secret leaked in result")
			}
		})
	}
}

func TestInteractiveSelectionOverridesEnvironmentOnlyForThisInstance(t *testing.T) {
	for _, env := range config.EnvironmentVariables() {
		t.Setenv(env, "")
	}
	t.Setenv(config.EnvModel, "environment-model")
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"file-model"}`)
	initial, err := config.LoadFiles(paths, config.LoadOptions{Environment: true})
	if err != nil {
		t.Fatal(err)
	}
	model, options, err := resolveModelSettings(defaultProviders(), initial)
	if err != nil {
		t.Fatal(err)
	}
	runner := &interactiveSession{configuration: initial, model: model, options: options, providers: defaultProviders(), application: &application{dependencies: dependencies{saveSettings: config.SaveSettingsFile}}}
	message, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "model", Arguments: "selected-model"})
	if err != nil || runner.configuration.Model != "selected-model" || initial.Model != "environment-model" || !strings.Contains(message, "next startup") {
		t.Fatalf("runtime selection: %s / %v", message, err)
	}
	restarted, err := config.LoadFiles(paths, config.LoadOptions{Environment: true})
	if err != nil || restarted.Model != "environment-model" {
		t.Fatalf("restart priority: %v", err)
	}
	data, err := os.ReadFile(paths.GlobalSettings)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["model"] != "selected-model" || bytes.Contains(data, []byte("environment-model")) {
		t.Fatal("wrong persisted source")
	}
}
