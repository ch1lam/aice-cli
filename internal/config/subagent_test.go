package config_test

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// The subagents' model and thinking load from settings and environment, are
// editable settings, and reject a level AICE does not know.
func TestSubagentModelSettings(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"subagent_model": "moonshot/kimi-k3", "subagent_thinking": "low"})
	loaded, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil || loaded.SubagentModel != "moonshot/kimi-k3" || loaded.SubagentThinking != llm.ThinkingLevelLow {
		t.Fatalf("loaded = %q / %q, %v", loaded.SubagentModel, loaded.SubagentThinking, err)
	}
	loaded, err = config.LoadFiles(paths, environmentOptions(t, map[string]string{"AICE_SUBAGENT_MODEL": "deepseek-v4-pro", "AICE_SUBAGENT_THINKING": "max"}))
	if err != nil || loaded.SubagentModel != "deepseek-v4-pro" || loaded.SubagentThinking != llm.ThinkingLevelMax {
		t.Fatalf("environment = %q / %q, %v", loaded.SubagentModel, loaded.SubagentThinking, err)
	}
	value, err := config.ParseSettingValue(config.SettingSubagentThinking, "high")
	if err != nil {
		t.Fatal(err)
	}
	patched, err := loaded.WithPatch(config.SettingsPatch{Changes: []config.SettingChange{{ID: config.SettingSubagentThinking, Value: value}}})
	if err != nil || patched.SubagentThinking != llm.ThinkingLevelHigh {
		t.Fatalf("patched = %q, %v", patched.SubagentThinking, err)
	}
	writeJSON(t, paths.GlobalSettings, map[string]any{"subagent_thinking": "huge"})
	if _, err := config.LoadFiles(paths, environmentOptions(t, nil)); err == nil || !strings.Contains(err.Error(), "subagent_thinking") {
		t.Fatalf("invalid level accepted: %v", err)
	}
}
