package config_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestRunLimitsLayeringAndSnapshot(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), "project.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"run_token_budget": 100, "run_timeout": "1m", "run_no_progress_limit": 10, "max_turns": 10})
	writeJSON(t, paths.ProjectSettings, map[string]any{"run_token_budget": 200, "run_no_progress_limit": 12, "max_turns": 20})
	options := environmentOptions(t, map[string]string{"AICE_RUN_TOKEN_BUDGET": "300", "AICE_RUN_TIMEOUT": "2m", "AICE_RUN_NO_PROGRESS_LIMIT": "15", "AICE_MAX_TURNS": "30"})
	cmd := &cobra.Command{}
	cmd.Flags().Int64("run-token-budget", 0, "")
	cmd.Flags().Duration("run-timeout", 0, "")
	cmd.Flags().Int("run-no-progress-limit", 8, "")
	cmd.Flags().Int("max-turns", 0, "")
	options.BindFlags = func(v *viper.Viper) error {
		if err := v.BindPFlag("run_token_budget", cmd.Flags().Lookup("run-token-budget")); err != nil {
			return err
		}
		if err := v.BindPFlag("run_timeout", cmd.Flags().Lookup("run-timeout")); err != nil {
			return err
		}
		if err := v.BindPFlag("run_no_progress_limit", cmd.Flags().Lookup("run-no-progress-limit")); err != nil {
			return err
		}
		return v.BindPFlag("max_turns", cmd.Flags().Lookup("max-turns"))
	}
	got, err := config.LoadFiles(paths, options)
	if err != nil || got.RunTokenBudget != 300 || got.RunTimeout != 2*time.Minute || got.RunNoProgressLimit != 15 || got.MaxTurns != 30 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	positive, err := got.WithSettings(map[config.Setting]string{config.SettingThinking: "low"})
	if err != nil || positive.MaxTurns != 30 {
		t.Fatal("runtime change lost positive turn limit", err)
	}
	if err := cmd.Flags().Set("max-turns", "0"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("run-token-budget", "0"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("run-timeout", "0s"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("run-no-progress-limit", "0"); err != nil {
		t.Fatal(err)
	}
	got, err = config.LoadFiles(paths, options)
	if err != nil || got.RunTokenBudget != 0 || got.RunTimeout != 0 || got.RunNoProgressLimit != 0 || got.MaxTurns != 0 {
		t.Fatalf("zero override err=%v", err)
	}
	next, err := got.WithSettings(map[config.Setting]string{config.SettingThinking: "low"})
	if err != nil || next.RunTokenBudget != 0 || next.RunTimeout != 0 || next.RunNoProgressLimit != 0 || next.MaxTurns != 0 {
		t.Fatal("runtime model changes lost limits", err)
	}
}

func TestRunLimitsValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		values map[string]any
	}{
		{"negative max turns", map[string]any{"max_turns": -1}},
		{"fractional max turns", map[string]any{"max_turns": 1.5}},
		{"overflow max turns", map[string]any{"max_turns": "9223372036854775808"}},
		{"negative repetition limit", map[string]any{"run_no_progress_limit": -1}},
		{"repetition limit one", map[string]any{"run_no_progress_limit": 1}},
		{"negative tokens", map[string]any{"run_token_budget": -1}},
		{"fractional tokens", map[string]any{"run_token_budget": 1.5}},
		{"overflow", map[string]any{"run_token_budget": "9223372036854775808"}},
		{"negative timeout", map[string]any{"run_timeout": "-1s"}},
		{"invalid timeout", map[string]any{"run_timeout": "tomorrow"}},
		{"numeric timeout", map[string]any{"run_timeout": 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := testPaths(t.TempDir())
			writeJSON(t, paths.GlobalSettings, tc.values)
			if _, err := config.LoadFiles(paths, config.LoadOptions{}); err == nil {
				t.Fatal("invalid limit accepted")
			}
		})
	}
	paths := testPaths(t.TempDir())
	got, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || got.RunTokenBudget != 0 || got.RunTimeout != 0 || got.RunNoProgressLimit != 8 || got.MaxTurns != 0 {
		t.Fatal("unexpected run defaults", err)
	}
}
