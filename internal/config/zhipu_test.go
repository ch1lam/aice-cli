package config_test

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestLoadFilesResolvesZhipuCredentials(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"zhipu_api_key": "file-key",
	})

	values := map[string]string{
		config.EnvZhipuAPIKey:  "environment-key",
		config.EnvZhipuBaseURL: " https://zhipu.example/v1 ",
	}
	got, err := config.LoadFiles(paths, mapLookup(values))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.ZhipuAPIKey != "environment-key" {
		t.Errorf("ZhipuAPIKey = %q, want environment-key", got.ZhipuAPIKey)
	}
	if got.ZhipuBaseURL != "https://zhipu.example/v1" {
		t.Errorf(
			"ZhipuBaseURL = %q, want trimmed custom URL",
			got.ZhipuBaseURL,
		)
	}
}

func TestSaveZhipuAPIKeyFilePreservesOtherProviderKeys(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	if err := config.SaveDeepSeekAPIKeyFile(paths, "deepseek-key"); err != nil {
		t.Fatalf("SaveDeepSeekAPIKeyFile() error = %v", err)
	}
	if err := config.SaveOpenCodeAPIKeyFile(paths, "opencode-key"); err != nil {
		t.Fatalf("SaveOpenCodeAPIKeyFile() error = %v", err)
	}
	if err := config.SaveZhipuAPIKeyFile(paths, " zhipu-key "); err != nil {
		t.Fatalf("SaveZhipuAPIKeyFile() error = %v", err)
	}

	var auth map[string]string
	readJSON(t, paths.GlobalAuth, &auth)
	want := map[string]string{
		"deepseek_api_key": "deepseek-key",
		"opencode_api_key": "opencode-key",
		"zhipu_api_key":    "zhipu-key",
	}
	if !reflect.DeepEqual(auth, want) {
		t.Errorf("auth = %#v, want %#v", auth, want)
	}
}

func TestSaveZhipuAPIKeyFileRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	for _, value := range []string{"", "  ", "line-one\nline-two"} {
		err := config.SaveZhipuAPIKeyFile(paths, value)
		if err == nil {
			t.Fatalf("SaveZhipuAPIKeyFile(%q) error = nil", value)
		}
	}
}
