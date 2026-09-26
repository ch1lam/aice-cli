package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/trust"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func settingPatch(t *testing.T, id config.Setting, text string) config.SettingsPatch {
	t.Helper()
	value, err := config.ParseSettingValue(id, text)
	if err != nil {
		t.Fatal(err)
	}
	return config.SettingsPatch{Changes: []config.SettingChange{{ID: id, Value: value}}}
}

func TestSettingsPatchFrozenSourcesAndInheritance(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), "project.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "user", "max_turns": 3})
	writeJSON(t, paths.GlobalAuth, map[string]any{"model": "auth"})
	writeJSON(t, paths.ProjectSettings, map[string]any{"model": "project"})
	opts := environmentOptions(t, map[string]string{config.EnvModel: "env"})
	cmd := &cobra.Command{}
	cmd.Flags().String("model", "ignored-default", "")
	opts.BindFlags = func(v *viper.Viper) error { return v.BindPFlag("model", cmd.Flags().Lookup("model")) }
	c, err := config.LoadFiles(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	state, err := c.SettingState(config.SettingModel)
	if err != nil || state.Source.Kind != "env" || state.Source.Location != config.EnvModel || state.Saved.Text != "user" || state.Inherited.Text != "env" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err := cmd.Flags().Set("model", "flag"); err != nil {
		t.Fatal(err)
	}
	c, err = config.LoadFiles(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	state, err = c.SettingState(config.SettingModel)
	if err != nil || state.Source.Kind != "flag" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	selected, err := c.WithPatch(settingPatch(t, config.SettingModel, "selected"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvModel, "later-env")
	if err := cmd.Flags().Set("model", "later-flag"); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "peer", "max_turns": 99})
	reset, err := selected.WithPatch(config.SettingsPatch{Changes: []config.SettingChange{{ID: config.SettingModel, Unset: true}}})
	if err != nil || reset.Model != "flag" || reset.MaxTurns != 3 || selected.Model != "selected" {
		t.Fatalf("reset=%s/%d err=%v", reset.Model, reset.MaxTurns, err)
	}
	state, err = selected.SettingState(config.SettingModel)
	if err != nil || state.Source.Kind != "runtime" || state.Saved.Text != "selected" || state.Inherited.Text != "flag" {
		t.Fatalf("selected state=%+v err=%v", state, err)
	}
}

func TestSettingsPatchExplicitZeroEmptyAndDiskIsolation(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"browser_headed": true, "run_token_budget": 42, "model": "old", "thinking": "low"})
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent writer changes an unrelated preference; persist merges it,
	// whereas publication retains the local frozen value.
	writeJSON(t, paths.GlobalSettings, map[string]any{"browser_headed": true, "run_token_budget": 42, "model": "old", "thinking": "high", "unknown": "preserve"})
	patch := config.SettingsPatch{}
	for id, text := range map[config.Setting]string{"browser_headed": "false", "run_token_budget": "0", "model": "", "run_timeout": "1.000000001s"} {
		patch.Changes = append(patch.Changes, settingPatch(t, id, text).Changes...)
	}
	patch.Changes = append(patch.Changes, config.SettingChange{ID: "context_windows", Value: config.SettingValue{Kind: config.ContextWindowsValue, Windows: []config.ContextWindow{}}})
	next, err := c.WithPatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	result, err := config.SaveSettingsPatch(t.Context(), paths, patch)
	if err != nil || !result.Committed {
		t.Fatalf("save=%+v err=%v", result, err)
	}
	if next.BrowserHeaded || next.RunTokenBudget != 0 || next.Model != "" || next.Thinking != "low" || next.RunTimeout != time.Second+time.Nanosecond {
		t.Fatal("candidate imported peer data or lost explicit zero/precision")
	}
	var disk map[string]any
	readJSON(t, paths.GlobalSettings, &disk)
	if disk["browser_headed"] != false || disk["run_token_budget"] != float64(0) || disk["model"] != "" || disk["thinking"] != "high" || disk["unknown"] != "preserve" || disk["run_timeout"] != "1.000000001s" {
		t.Fatalf("disk=%v", disk)
	}
	unset := config.SettingsPatch{Changes: []config.SettingChange{{ID: config.SettingModel, Unset: true}}}
	if _, err := config.SaveSettingsPatch(t.Context(), paths, unset); err != nil {
		t.Fatal(err)
	}
	disk = nil
	readJSON(t, paths.GlobalSettings, &disk)
	if _, ok := disk["model"]; ok {
		t.Fatal("unset stored empty instead of deleting")
	}
}

func TestSettingsPatchRejectsInvalidAndUntrustedInheritance(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), "project.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"max_turns": 5})
	writeJSON(t, paths.ProjectSettings, map[string]any{"max_turns": -1})
	c, err := config.LoadFiles(paths, config.LoadOptions{TrustProject: func(config.Paths, trust.Default) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	reset, err := c.WithPatch(config.SettingsPatch{Changes: []config.SettingChange{{ID: "max_turns", Unset: true}}})
	if err != nil || reset.MaxTurns != 0 {
		t.Fatalf("untrusted candidate=%d err=%v", reset.MaxTurns, err)
	}
	// A runtime choice can mask an invalid lower layer only if the startup
	// winner was valid. Removing it must expose, rather than repair, the error.
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": 3})
	writeJSON(t, paths.GlobalAuth, map[string]any{"model": "valid"})
	c, err = config.LoadFiles(paths, config.LoadOptions{TrustProject: func(config.Paths, trust.Default) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	state, err := c.SettingState(config.SettingModel)
	if err != nil || state.Source.Kind != "user-auth" || state.Inherited.Text != "valid" {
		t.Fatalf("auth state=%+v err=%v", state, err)
	}
	for _, patch := range []config.SettingsPatch{
		{Changes: []config.SettingChange{{ID: "max_turns", Value: config.SettingValue{Kind: config.IntValue, Int: -1}}}},
		{Changes: []config.SettingChange{{ID: "max_turns", Value: config.SettingValue{Kind: config.BoolValue}}}},
		{Changes: []config.SettingChange{{ID: "max_turns", Value: config.SettingValue{Kind: config.IntValue, Text: "wrong"}}}},
		{Changes: []config.SettingChange{{ID: "model", Unset: true, Value: config.SettingValue{Kind: config.EnumValue}}}},
		{Changes: []config.SettingChange{{ID: "openai_api_key", Value: config.SettingValue{Kind: config.StringValue, Text: "secret"}}}},
	} {
		if _, err := c.WithPatch(patch); err == nil {
			t.Fatal("accepted invalid patch")
		}
		if result, err := config.SaveSettingsPatch(t.Context(), paths, patch); err == nil || result.Committed {
			t.Fatal("saved invalid patch")
		}
	}
}

func TestSettingsPatchKeepsNewCredentials(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{"anthropic_api_key": "old"})
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.AnthropicAPIKey = "new"
	c.ClaudeSubscriptionCredentials.AccessToken = "new-oauth"
	next, err := c.WithPatch(settingPatch(t, "run_timeout", "2m"))
	if err != nil || next.AnthropicAPIKey != "new" || next.ClaudeSubscriptionCredentials.AccessToken != "new-oauth" {
		t.Fatal("preference restored old credential", err)
	}
	c.AnthropicAPIKey = ""
	next, err = c.WithPatch(settingPatch(t, "run_timeout", "2m"))
	if err != nil || next.AnthropicAPIKey != "" {
		t.Fatal("cleared credential returned", err)
	}
}

func TestSettingsPatchCanceledAndDamagedFile(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "old"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := config.SaveSettingsPatch(ctx, paths, settingPatch(t, "model", "new"))
	if err == nil || result.Committed {
		t.Fatal("canceled save committed")
	}
	if err := os.WriteFile(paths.GlobalSettings, []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = config.SaveSettingsPatch(t.Context(), paths, settingPatch(t, "model", "new"))
	if err == nil || result.Committed {
		t.Fatal("damaged file replaced")
	}
}

func TestWebPatchPreservesPeerInstanceFieldsAndEmptyList(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": map[string]any{"services": map[string]any{"exa-main": map[string]any{"provider": "exa", "base_url": "https://peer.example", "credential": map[string]any{"env": "EXA_API_KEY"}, "options": map[string]any{"type": "fast"}}}}})
	enabled := false
	empty := []string{}
	patch := config.WebPatch{ServiceEdits: map[string]config.WebServicePatch{"exa-main": {Enabled: &enabled}}, AllowedDomains: &empty}
	result, err := config.SaveWebPatch(t.Context(), paths, patch)
	if err != nil || !result.Committed {
		t.Fatal(result, err)
	}
	var disk map[string]any
	readJSON(t, paths.GlobalSettings, &disk)
	web := disk["web"].(map[string]any)
	service := web["services"].(map[string]any)["exa-main"].(map[string]any)
	if service["base_url"] != "https://peer.example" || service["enabled"] != false || service["options"].(map[string]any)["type"] != "fast" {
		t.Fatal(service)
	}
	if domains, ok := web["search"].(map[string]any)["allowed_domains"].([]any); !ok || len(domains) != 0 {
		t.Fatal("empty list became absent/null")
	}
}

func TestSettingsCannotLiftPreviouslyRedundantProjectWebRestriction(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), "project.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": map[string]any{"search": map[string]any{"enabled": false}}})
	writeJSON(t, paths.ProjectSettings, map[string]any{"web": map[string]any{"search": map[string]any{"enabled": false}}})
	c, err := config.LoadFiles(paths, config.LoadOptions{TrustProject: func(config.Paths, trust.Default) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	next, err := c.WithWebPatch(config.WebPatch{SearchEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if next.Web.SearchEnabled {
		t.Fatal("user save lifted trusted project restriction")
	}
}
