package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func TestLoadFilesAppliesGlobalAndEnvironmentPrecedence(t *testing.T) {

	root := t.TempDir()
	paths := testPaths(root)
	writeJSON(t, paths.GlobalSettings, map[string]any{
		"provider": "global-provider",
		"model":    "global-model",
		"thinking": "low",
	})
	writeJSON(t, filepath.Join(
		root,
		"workspace",
		".aice",
		"settings.json",
	), map[string]any{
		"provider": "project-provider",
		"model":    "project-model",
	})
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"deepseek_api_key": "file-key",
	})

	values := map[string]string{
		config.EnvModel:           "environment-model",
		config.EnvThinking:        "high",
		config.EnvDeepSeekAPIKey:  "environment-key",
		config.EnvDeepSeekBaseURL: " https://deepseek.example/anthropic ",
	}
	got, err := config.LoadFiles(paths, environmentOptions(t, values))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.Provider != "global-provider" {
		t.Errorf("Provider = %q, want global-provider", got.Provider)
	}
	if got.Model != "environment-model" {
		t.Errorf("Model = %q, want environment-model", got.Model)
	}
	if got.Thinking != llm.ThinkingLevelHigh {
		t.Errorf("Thinking = %q, want high", got.Thinking)
	}
	if got.APIKeys["deepseek"] != "environment-key" {
		t.Errorf("DeepSeekAPIKey = %q, want environment-key", got.APIKeys["deepseek"])
	}
	if got.BaseURLs["deepseek"] != "https://deepseek.example/anthropic" {
		t.Errorf(
			"DeepSeekBaseURL = %q, want trimmed custom URL",
			got.BaseURLs["deepseek"],
		)
	}
	if got.Paths != paths {
		t.Errorf("Paths = %#v, want %#v", got.Paths, paths)
	}
}

func TestLoadFilesUsesGlobalAuthAndOptionalSettings(t *testing.T) {

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"deepseek_api_key": " file-key ",
	})

	got, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.APIKeys["deepseek"] != "file-key" {
		t.Errorf("DeepSeekAPIKey = %q, want file-key", got.APIKeys["deepseek"])
	}
	if got.Provider != "" ||
		got.Model != "" ||
		got.Thinking != llm.ThinkingLevelMedium {
		t.Errorf("settings = %#v, want inherited defaults", got)
	}
}

func TestLoadFilesAllowsMissingCredentials(t *testing.T) {

	paths := testPaths(t.TempDir())
	got, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.APIKeys["deepseek"] != "" {
		t.Errorf("DeepSeekAPIKey = %q, want unconfigured", got.APIKeys["deepseek"])
	}
	if got.Paths != paths {
		t.Errorf("Paths = %#v, want %#v", got.Paths, paths)
	}
}

func TestLoadFilesDefaultProjectTrustDefaultsToAsk(t *testing.T) {

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{
		"provider": "deepseek",
	})
	got, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.DefaultProjectTrust != trust.DefaultAsk {
		t.Errorf(
			"DefaultProjectTrust = %q, want ask",
			got.DefaultProjectTrust,
		)
	}
}

func TestLoadFilesReadsDefaultProjectTrust(t *testing.T) {

	for _, want := range []trust.Default{
		trust.DefaultAlways,
		trust.DefaultNever,
	} {
		paths := testPaths(t.TempDir())
		writeJSON(t, paths.GlobalSettings, map[string]any{
			"default_project_trust": string(want),
		})
		got, err := config.LoadFiles(paths, environmentOptions(t, nil))
		if err != nil {
			t.Fatalf("LoadFiles() error = %v", err)
		}
		if got.DefaultProjectTrust != want {
			t.Errorf(
				"DefaultProjectTrust = %q, want %q",
				got.DefaultProjectTrust,
				want,
			)
		}
	}
}

func TestLoadFilesRejectsInvalidDefaultProjectTrust(t *testing.T) {

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{
		"default_project_trust": "sometimes",
	})
	_, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err == nil ||
		!strings.Contains(err.Error(), "unsupported default project trust") {
		t.Fatalf("LoadFiles() error = %v, want unsupported trust error", err)
	}
}

func TestLoadFilesRejectsInvalidFiles(t *testing.T) {

	tests := []struct {
		name    string
		file    string
		content string
		want    string
	}{
		{
			name:    "unknown setting",
			file:    "settings",
			content: `{"unknown":true}`,
			want:    "unknown field",
		},
		{
			name:    "invalid thinking level",
			file:    "settings",
			content: `{"thinking":"extreme"}`,
			want:    "unsupported thinking level",
		},
		{
			name:    "unknown auth field",
			file:    "auth",
			content: `{"api_key":"secret"}`,
			want:    "unknown field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			paths := testPaths(t.TempDir())
			path := paths.GlobalSettings
			if tt.file == "auth" {
				path = paths.GlobalAuth
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, err := config.LoadFiles(paths, environmentOptions(t, map[string]string{
				config.EnvDeepSeekAPIKey: "test-key",
			}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadFiles() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSaveSettingFileUpdatesGlobalSettings(t *testing.T) {

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{
		"provider": "deepseek",
		"model":    "global-model",
		"thinking": "low",
	})

	if err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{
		config.SettingModel: "updated-model",
	}); err != nil {
		t.Fatalf("SaveSettingsFile() error = %v", err)
	}

	var global config.Settings
	readJSON(t, paths.GlobalSettings, &global)
	if !reflect.DeepEqual(global, config.Settings{
		Provider: "deepseek",
		Model:    "updated-model",
		Thinking: llm.ThinkingLevelLow,
	}) {
		t.Errorf(
			"global settings = %#v, want preserved provider and thinking",
			global,
		)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(paths.GlobalSettings)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("global settings mode = %#o, want 0600", got)
		}
	}
}

func TestSaveSettingFileValidatesInput(t *testing.T) {

	paths := testPaths(t.TempDir())
	tests := []struct {
		name    string
		setting config.Setting
		value   string
		want    string
	}{
		{
			name:    "setting",
			setting: "temperature",
			value:   "1",
			want:    "unsupported setting",
		},
		{
			name:    "thinking",
			setting: config.SettingThinking,
			value:   "extreme",
			want:    "unsupported thinking level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{
				tt.setting: tt.value,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("SaveSettingsFile() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func testPaths(root string) config.Paths {
	return config.Paths{
		GlobalSettings: filepath.Join(root, "global", "settings.json"),
		GlobalAuth:     filepath.Join(root, "global", "auth.json"),
		GlobalTrust:    filepath.Join(root, "global", "trust.json"),
		BinDir:         filepath.Join(root, "global", "bin"),
	}
}

func environmentOptions(t *testing.T, values map[string]string) config.LoadOptions {
	t.Helper()
	for _, env := range config.EnvironmentVariables() {
		t.Setenv(env, "")
	}
	for key, value := range values {
		t.Setenv(key, strings.TrimSpace(value))
	}
	return config.LoadOptions{Environment: true}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
}

func TestContextWindowsLoadAndSurviveSettingChanges(t *testing.T) {

	paths := testPaths(t.TempDir())
	windows := map[string]int64{"openai-codex/gpt-5.6-terra": 272000, "custom/Org/Model.v1": 32768}
	writeJSON(t, paths.GlobalSettings, config.Settings{ContextWindows: []config.ContextWindow{{Provider: "openai-codex", Model: "gpt-5.6-terra", Tokens: 272000}, {Provider: "custom", Model: "Org/Model.v1", Tokens: 32768}}})
	if err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{config.SettingModel: "different"}); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ContextWindows, windows) {
		t.Fatalf("windows = %#v", got.ContextWindows)
	}
}

func TestContextWindowsRejectInvalidLimits(t *testing.T) {

	for _, content := range []string{
		`{"context_windows":[{"model":"model","tokens":200000}]}`,
		`{"context_windows":[{"provider":"custom","tokens":200000}]}`,
		`{"context_windows":[{"provider":"custom","model":"model","tokens":0}]}`,
		`{"context_windows":[{"provider":"custom","model":"model","tokens":-1}]}`,
		`{"context_windows":[{"provider":"custom","model":"model","tokens":1.5}]}`,
	} {
		paths := testPaths(t.TempDir())
		if err := os.MkdirAll(filepath.Dir(paths.GlobalSettings), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.GlobalSettings, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFiles(paths, environmentOptions(t, nil)); err == nil {
			t.Fatalf("accepted %s", content)
		}
	}
}
