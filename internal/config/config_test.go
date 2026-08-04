package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestLoadFilesAppliesGlobalAndEnvironmentPrecedence(t *testing.T) {
	t.Parallel()

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
	got, err := config.LoadFiles(paths, mapLookup(values))
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
	if got.DeepSeekAPIKey != "environment-key" {
		t.Errorf("DeepSeekAPIKey = %q, want environment-key", got.DeepSeekAPIKey)
	}
	if got.DeepSeekBaseURL != "https://deepseek.example/anthropic" {
		t.Errorf(
			"DeepSeekBaseURL = %q, want trimmed custom URL",
			got.DeepSeekBaseURL,
		)
	}
	if got.Paths != paths {
		t.Errorf("Paths = %#v, want %#v", got.Paths, paths)
	}
}

func TestLoadFilesUsesGlobalAuthAndOptionalSettings(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"deepseek_api_key": " file-key ",
	})

	got, err := config.LoadFiles(paths, mapLookup(nil))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.DeepSeekAPIKey != "file-key" {
		t.Errorf("DeepSeekAPIKey = %q, want file-key", got.DeepSeekAPIKey)
	}
	if got.Provider != "" ||
		got.Model != "" ||
		got.Thinking != llm.ThinkingLevelUnknown {
		t.Errorf("settings = %#v, want inherited defaults", got)
	}
}

func TestLoadFilesAllowsMissingCredentials(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	got, err := config.LoadFiles(paths, mapLookup(nil))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.DeepSeekAPIKey != "" {
		t.Errorf("DeepSeekAPIKey = %q, want unconfigured", got.DeepSeekAPIKey)
	}
	if got.Paths != paths {
		t.Errorf("Paths = %#v, want %#v", got.Paths, paths)
	}
}

func TestLoadFilesRejectsInvalidFiles(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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

			_, err := config.LoadFiles(paths, mapLookup(map[string]string{
				config.EnvDeepSeekAPIKey: "test-key",
			}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadFiles() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSaveSettingFileUpdatesGlobalSettings(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{
		"provider": "deepseek",
		"model":    "global-model",
		"thinking": "low",
	})

	if err := config.SaveSettingFile(
		paths,
		config.SettingModel,
		"updated-model",
	); err != nil {
		t.Fatalf("SaveSettingFile() error = %v", err)
	}

	var global config.Settings
	readJSON(t, paths.GlobalSettings, &global)
	if global != (config.Settings{
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
	t.Parallel()

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
			t.Parallel()

			err := config.SaveSettingFile(
				paths,
				tt.setting,
				tt.value,
			)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("SaveSettingFile() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSaveDeepSeekAPIKeyFileWritesGlobalCredentialOnly(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	if err := config.SaveDeepSeekAPIKeyFile(paths, " test-key "); err != nil {
		t.Fatalf("SaveDeepSeekAPIKeyFile() error = %v", err)
	}

	var auth map[string]string
	readJSON(t, paths.GlobalAuth, &auth)
	if got := auth["deepseek_api_key"]; got != "test-key" {
		t.Errorf("saved API key = %q, want test-key", got)
	}
	if _, err := os.Stat(paths.GlobalSettings); !os.IsNotExist(err) {
		t.Fatalf(
			"global settings stat error = %v, want not exist",
			err,
		)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(paths.GlobalAuth)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("global auth mode = %#o, want 0600", got)
		}
	}
}

func TestSaveDeepSeekAPIKeyFileRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	for _, value := range []string{"", "  ", "line-one\nline-two"} {
		err := config.SaveDeepSeekAPIKeyFile(paths, value)
		if err == nil {
			t.Fatalf("SaveDeepSeekAPIKeyFile(%q) error = nil", value)
		}
	}
}

func TestLoadFilesRejectsNilLookup(t *testing.T) {
	t.Parallel()

	_, err := config.LoadFiles(testPaths(t.TempDir()), nil)
	if err == nil || !strings.Contains(err.Error(), "lookup is required") {
		t.Fatalf("LoadFiles() error = %v, want missing lookup error", err)
	}
}

func testPaths(root string) config.Paths {
	return config.Paths{
		GlobalSettings: filepath.Join(root, "global", "settings.json"),
		GlobalAuth:     filepath.Join(root, "global", "auth.json"),
		BinDir:         filepath.Join(root, "global", "bin"),
	}
}

func mapLookup(values map[string]string) config.LookupEnv {
	return func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	}
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
