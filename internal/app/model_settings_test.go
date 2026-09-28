package app

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/provider/opencode"
)

// The panel and slash entry points must preserve the same selection and
// persistence behavior, including failure before publication and late cleanup.
func TestModelSettingsEntryPoints(t *testing.T) {
	t.Parallel()
	for _, selection := range []struct {
		name, field, input, provider, model string
		requested, effective                llm.ThinkingLevel
		changes                             map[config.Setting]string
		rebuild                             bool
		wantError                           string
	}{
		{
			name: "provider fallback", field: "provider", input: "deepseek",
			provider: "deepseek", model: deepseek.DefaultModel().ID,
			requested: llm.ThinkingLevelXHigh, effective: llm.ThinkingLevelHigh,
			changes: map[config.Setting]string{config.SettingProvider: "deepseek", config.SettingModel: deepseek.DefaultModel().ID}, rebuild: true,
		},
		{
			name: "model preserves requested thinking", field: "model", input: opencode.DefaultModel().ID,
			provider: "opencode-go", model: opencode.DefaultModel().ID,
			requested: llm.ThinkingLevelXHigh, effective: llm.ThinkingLevelMax,
			changes: map[config.Setting]string{config.SettingModel: opencode.DefaultModel().ID},
		},
		{
			name: "thinking trims input", field: "thinking", input: " \t low \n",
			provider: "opencode-go", model: "gpt-5.6-luna",
			requested: llm.ThinkingLevelLow, effective: llm.ThinkingLevelLow,
			changes: map[config.Setting]string{config.SettingThinking: "low"},
		},
		{name: "embedded whitespace", field: "model", input: "two models", wantError: "app: usage: /model <model>"},
		{name: "unsupported provider", field: "provider", input: "missing", wantError: "unsupported provider"},
		{name: "unsupported model", field: "model", input: "missing", wantError: "unsupported model"},
		{name: "unsupported thinking", field: "thinking", input: "extreme", wantError: "unsupported thinking level"},
	} {
		for _, entry := range []string{"slash", "settings"} {
			for _, persistence := range []string{"saved", "save failure", "cleanup warning"} {
				if selection.wantError != "" && persistence != "saved" {
					continue
				}
				t.Run(selection.name+"/"+entry+"/"+persistence, func(t *testing.T) {
					t.Parallel()
					configuration := config.Config{
						Provider: "opencode-go", Model: "gpt-5.6-luna", Thinking: llm.ThinkingLevelXHigh,
						DeepSeekAPIKey: "synthetic-key", OpenCodeAPIKey: "synthetic-key",
					}
					model, options, err := resolveModelSettings(defaultProviders(), configuration)
					if err != nil {
						t.Fatal(err)
					}
					originalLoop := new(agent.Loop)
					s := &interactiveSession{providers: defaultProviders(), configuration: configuration, model: model, options: options, loop: originalLoop}
					saves, rebuilds := 0, 0
					var saved map[config.Setting]string
					failure := errors.New("synthetic settings failure")
					s.application = &application{dependencies: dependencies{
						providers: defaultProviders(),
						newModel:  func(config.Config) (llm.Streamer, error) { rebuilds++; return &recordingModel{}, nil },
						saveSettings: func(_ context.Context, _ config.Paths, changes map[config.Setting]string) error {
							saves++
							saved = maps.Clone(changes)
							if !reflect.DeepEqual(s.configuration, configuration) || s.loop != originalLoop || !reflect.DeepEqual(s.model, model) || !reflect.DeepEqual(s.options, options) {
								t.Error("selection published before persistence")
							}
							if _, err := s.beginPreparation(); !errors.Is(err, interaction.ErrSettingsBusy) {
								t.Errorf("preparation during save = %v", err)
							}
							switch persistence {
							case "save failure":
								return failure
							case "cleanup warning":
								return &config.CommittedError{Warning: failure}
							}
							return nil
						},
					}}
					var output string
					var result interaction.SettingsResult
					if entry == "slash" {
						output, err = s.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: selection.field, Arguments: selection.input})
					} else {
						result, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: selection.field, Value: interaction.SettingValue{Kind: interaction.SettingEnum, Text: selection.input}}}})
					}
					revision, reason := s.settingsStatus()
					if reason != "" {
						t.Fatalf("operation reservation leaked: %s", reason)
					}
					if selection.wantError != "" || persistence == "save failure" {
						if err == nil || (selection.wantError != "" && !strings.Contains(err.Error(), selection.wantError)) || (selection.wantError == "" && !errors.Is(err, failure)) {
							t.Fatalf("error = %v", err)
						}
						if revision != 0 || result.Committed || result.Applied || !reflect.DeepEqual(s.configuration, configuration) || s.loop != originalLoop || !reflect.DeepEqual(s.model, model) || !reflect.DeepEqual(s.options, options) {
							t.Fatal("failed selection published state")
						}
						if selection.wantError != "" && (saves != 0 || rebuilds != 0) {
							t.Fatal("invalid input prepared or saved")
						}
						if entry == "settings" && result.FieldErrors[selection.field] != err.Error() {
							t.Fatal("panel lost field error")
						}
						return
					}
					if err != nil || revision != 1 || saves != 1 || !maps.Equal(saved, selection.changes) {
						t.Fatalf("save error=%v revision=%d saves=%d changes=%v", err, revision, saves, saved)
					}
					if s.configuration.Provider != selection.provider || s.model.ID != selection.model || s.configuration.Model != selection.model || s.configuration.Thinking != selection.requested || s.options.Thinking != selection.effective {
						t.Fatalf("selection = %s/%s requested=%s effective=%s", s.configuration.Provider, s.model.ID, s.configuration.Thinking, s.options.Thinking)
					}
					if (rebuilds == 1) != selection.rebuild || (s.loop != originalLoop) != selection.rebuild {
						t.Fatalf("unexpected loop replacement: %d", rebuilds)
					}
					if entry == "settings" {
						if !result.Committed || !result.Applied || result.Revision != 1 {
							t.Fatalf("panel result = %+v", result)
						}
						output = strings.Join(result.Warnings, "\n")
					} else if !strings.Contains(output, "Set "+selection.field+" to "+strings.TrimSpace(selection.input)) {
						t.Fatalf("selection message = %q", output)
					}
					if strings.Contains(output, "lock cleanup warning") != (persistence == "cleanup warning") {
						t.Fatalf("cleanup warning = %q", output)
					}
				})
			}
		}
	}
}
