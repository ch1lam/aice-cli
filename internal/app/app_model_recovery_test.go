package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInteractiveUnavailableModelRequiresSelection(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"test-key", ""} {
		name := "with credential"
		if key == "" {
			name = "without credential"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			configuration := config.Config{Provider: "deepseek", Model: "removed-model", DeepSeekAPIKey: key}
			service := &recordingModel{response: "done"}
			constructions, saves := 0, 0
			failSave := true
			command, err := newTestCommand(t, dependencies{
				loadConfig: func() (config.Config, error) { return configuration, nil },
				newModel: func(c config.Config) (llm.Streamer, error) {
					constructions++
					if c.Model != "deepseek-flash" {
						t.Fatalf("constructed model with %q", c.Model)
					}
					return service, nil
				},
				saveSetting: func(setting config.Setting, value string) error {
					if failSave {
						return errors.New("save failed")
					}
					if setting != config.SettingModel {
						t.Fatalf("unexpected save: %s", setting)
					}
					configuration.Model = value
					saves++
					return nil
				},
				runTUI: func(ctx context.Context, runner tui.Runner, options tui.Options) error {
					if options.Model.ID != "removed-model" || !strings.Contains(options.StartupNotice, "/model") {
						t.Fatalf("startup model/notice = %q/%q", options.Model.ID, options.StartupNotice)
					}
					s := runner.(*interactiveSession)
					assertBlocked := func() {
						t.Helper()
						// A missing attachment would report a file error if selection did not gate preparation first.
						active, err := runner.NewRun(ctx, interaction.RunInput{Prompt: "hello", Files: []string{"missing-file"}}, nil)
						if active != nil || err == nil || !strings.Contains(err.Error(), "/model") {
							t.Fatalf("NewRun = %v, %v", active, err)
						}
						if s.conversation.store != nil {
							t.Fatal("rejected prompt created Session storage")
						}
						if _, _, err := s.CreateSideThread("side question"); err == nil || !strings.Contains(err.Error(), "/model") {
							t.Fatalf("side error = %v", err)
						}
						if _, err := s.runInitCommand(ctx); err == nil || !strings.Contains(err.Error(), "/model") {
							t.Fatalf("init error = %v", err)
						}
					}
					assertBlocked()
					if constructions != 0 || saves != 0 {
						t.Fatal("startup constructed or saved a replacement")
					}
					for _, option := range s.modelMenu().Options {
						if option.Current {
							t.Fatal("unavailable model selected a menu default")
						}
					}
					if _, err := s.slashModel(ctx, interaction.CommandRequest{Name: "model", Arguments: "deepseek-flash"}); err == nil {
						t.Fatal("failed save accepted")
					}
					assertBlocked()
					failSave = false
					if _, err := s.slashModel(ctx, interaction.CommandRequest{Name: "model", Arguments: "deepseek-flash"}); err != nil {
						return err
					}
					if saves != 1 || s.RuntimeState().Model.ID != "deepseek-flash" {
						t.Fatal("selection was not saved and published")
					}
					if key == "" {
						_, err := runner.NewRun(ctx, interaction.RunInput{Prompt: "hello"}, nil)
						if err == nil || !strings.Contains(err.Error(), "API key") {
							t.Fatalf("missing credential error = %v", err)
						}
						if constructions != 0 {
							t.Fatal("constructed provider without credential")
						}
						return nil
					}
					return runInteractive(ctx, runner, "hello", nil)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			command.SetIn(strings.NewReader(""))
			command.SetOut(io.Discard)
			command.SetArgs([]string{"--workspace", t.TempDir(), "--no-approve"})
			if err := command.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if key != "" && (len(service.requests) != 1 || service.requests[0].Model.ID != "deepseek-flash") {
				t.Fatalf("requests = %#v", service.requests)
			}
		})
	}
}

func TestPrintUnavailableModelListsProviderChoices(t *testing.T) {
	t.Parallel()
	command, err := newTestCommand(t, dependencies{
		loadConfig: func() (config.Config, error) { return config.Config{Provider: "deepseek", Model: "removed-model"}, nil },
		newModel:   func(config.Config) (llm.Streamer, error) { t.Fatal("constructed unavailable model"); return nil, nil },
		runTUI:     func(context.Context, tui.Runner, tui.Options) error { t.Fatal("print opened TUI"); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	command.SetOut(io.Discard)
	command.SetArgs([]string{"--workspace", t.TempDir(), "--print", "hello"})
	err = command.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "available: deepseek-flash, deepseek-v4-pro") {
		t.Fatalf("print error = %v", err)
	}
}

func TestModelAvailabilityIsProviderScoped(t *testing.T) {
	t.Parallel()
	for _, providerID := range []string{"opencode-go", "custom"} {
		t.Run(providerID, func(t *testing.T) {
			model, _, err := resolveModelSettings(defaultProviders(), config.Config{Provider: providerID, Model: "deepseek-v4-flash"})
			if err != nil || model.ID != "deepseek-v4-flash" {
				t.Fatalf("model/error = %q/%v", model.ID, err)
			}
		})
	}
}
