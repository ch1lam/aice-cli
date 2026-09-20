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
	writeJSON(t, paths.GlobalSettings, map[string]any{"run_token_budget": 100, "run_timeout": "1m"})
	writeJSON(t, paths.ProjectSettings, map[string]any{"run_token_budget": 200})
	options := environmentOptions(t, map[string]string{"AICE_RUN_TOKEN_BUDGET": "300", "AICE_RUN_TIMEOUT": "2m"})
	cmd := &cobra.Command{}
	cmd.Flags().Int64("run-token-budget", 0, "")
	cmd.Flags().Duration("run-timeout", 0, "")
	options.BindFlags = func(v *viper.Viper) error {
		if err := v.BindPFlag("run_token_budget", cmd.Flags().Lookup("run-token-budget")); err != nil {
			return err
		}
		return v.BindPFlag("run_timeout", cmd.Flags().Lookup("run-timeout"))
	}
	got, err := config.LoadFiles(paths, options)
	if err != nil || got.RunTokenBudget != 300 || got.RunTimeout != 2*time.Minute {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if err := cmd.Flags().Set("run-token-budget", "0"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("run-timeout", "0s"); err != nil {
		t.Fatal(err)
	}
	got, err = config.LoadFiles(paths, options)
	if err != nil || got.RunTokenBudget != 0 || got.RunTimeout != 0 {
		t.Fatalf("zero override err=%v", err)
	}
	next, err := got.WithSettings(map[config.Setting]string{config.SettingThinking: "low"})
	if err != nil || next.RunTokenBudget != 0 || next.RunTimeout != 0 {
		t.Fatal("runtime model changes lost limits", err)
	}
}

func TestRunLimitsValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		values map[string]any
	}{
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
	if err != nil || got.RunTokenBudget != 0 || got.RunTimeout != 0 {
		t.Fatal("defaults must be unlimited", err)
	}
}
