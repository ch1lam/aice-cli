package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestLoadFilesResolvesAnthropicCredentials(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"anthropic_api_key": "file-key",
	})

	values := map[string]string{
		config.EnvAnthropicAPIKey:  "environment-key",
		config.EnvAnthropicBaseURL: " https://anthropic.example/v1 ",
	}
	got, err := config.LoadFiles(paths, environmentOptions(t, values))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.AnthropicAPIKey != "environment-key" {
		t.Errorf("AnthropicAPIKey = %q, want environment-key", got.AnthropicAPIKey)
	}
	if got.AnthropicBaseURL != "https://anthropic.example/v1" {
		t.Errorf(
			"AnthropicBaseURL = %q, want trimmed custom URL",
			got.AnthropicBaseURL,
		)
	}
	next, err := got.WithSettings(map[config.Setting]string{config.SettingModel: "claude-opus-5-5"})
	if err != nil || next.AnthropicAPIKey != got.AnthropicAPIKey || next.AnthropicBaseURL != got.AnthropicBaseURL {
		t.Fatalf("runtime selection lost connection settings: %v", err)
	}
}

func TestSaveAnthropicAPIKeyFilePreservesOtherProviderKeys(t *testing.T) {
	paths := testPaths(t.TempDir())
	if err := config.SaveDeepSeekAPIKeyFile(paths, "deepseek-key"); err != nil {
		t.Fatalf("SaveDeepSeekAPIKeyFile() error = %v", err)
	}
	if err := config.SaveOpenCodeAPIKeyFile(paths, "opencode-key"); err != nil {
		t.Fatalf("SaveOpenCodeAPIKeyFile() error = %v", err)
	}
	if err := config.SaveAnthropicAPIKeyFile(paths, " anthropic-key "); err != nil {
		t.Fatalf("SaveAnthropicAPIKeyFile() error = %v", err)
	}

	var auth map[string]string
	readJSON(t, paths.GlobalAuth, &auth)
	want := map[string]string{
		"deepseek_api_key":  "deepseek-key",
		"opencode_api_key":  "opencode-key",
		"anthropic_api_key": "anthropic-key",
	}
	if !reflect.DeepEqual(auth, want) {
		t.Errorf("auth = %#v, want %#v", auth, want)
	}
}

func TestSaveAnthropicAPIKeyFileRejectsInvalidValues(t *testing.T) {
	paths := testPaths(t.TempDir())
	for _, value := range []string{"", "  ", "line-one\nline-two"} {
		err := config.SaveAnthropicAPIKeyFile(paths, value)
		if err == nil {
			t.Fatalf("SaveAnthropicAPIKeyFile(%q) error = nil", value)
		}
	}
}

func TestAnthropicConnectionValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ field, value string }{
		{"anthropic_base_url", "file:///tmp/endpoint"},
		{"anthropic_api_key", "secret\ninvalid"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			paths := testPaths(t.TempDir())
			writeJSON(t, paths.GlobalSettings, map[string]string{tc.field: tc.value})
			_, err := config.LoadFiles(paths, config.LoadOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.field) || strings.Contains(err.Error(), tc.value) {
				t.Fatalf("validation should name field without leaking value: %v", err)
			}
		})
	}
}
