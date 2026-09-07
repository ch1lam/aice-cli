package config_test

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestLoadFilesResolvesZhipuCodingCredentials(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"zhipu_coding_api_key": "file-key",
	})

	values := map[string]string{
		config.EnvZhipuCodingAPIKey:  "environment-key",
		config.EnvZhipuCodingBaseURL: " https://zhipu_coding.example/v1 ",
	}
	got, err := config.LoadFiles(paths, mapLookup(values))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.ZhipuCodingAPIKey != "environment-key" {
		t.Errorf("ZhipuCodingAPIKey = %q, want environment-key", got.ZhipuCodingAPIKey)
	}
	if got.ZhipuCodingBaseURL != "https://zhipu_coding.example/v1" {
		t.Errorf(
			"ZhipuCodingBaseURL = %q, want trimmed custom URL",
			got.ZhipuCodingBaseURL,
		)
	}
}

func TestSaveZhipuCodingAPIKeyFilePreservesOtherProviderKeys(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	if err := config.SaveDeepSeekAPIKeyFile(paths, "deepseek-key"); err != nil {
		t.Fatalf("SaveDeepSeekAPIKeyFile() error = %v", err)
	}
	if err := config.SaveOpenCodeAPIKeyFile(paths, "opencode-key"); err != nil {
		t.Fatalf("SaveOpenCodeAPIKeyFile() error = %v", err)
	}
	if err := config.SaveZhipuCodingAPIKeyFile(paths, " zhipu_coding-key "); err != nil {
		t.Fatalf("SaveZhipuCodingAPIKeyFile() error = %v", err)
	}

	var auth map[string]string
	readJSON(t, paths.GlobalAuth, &auth)
	want := map[string]string{
		"deepseek_api_key":     "deepseek-key",
		"opencode_api_key":     "opencode-key",
		"zhipu_coding_api_key": "zhipu_coding-key",
	}
	if !reflect.DeepEqual(auth, want) {
		t.Errorf("auth = %#v, want %#v", auth, want)
	}
}

func TestSaveZhipuCodingAPIKeyFileRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	paths := testPaths(t.TempDir())
	for _, value := range []string{"", "  ", "line-one\nline-two"} {
		err := config.SaveZhipuCodingAPIKeyFile(paths, value)
		if err == nil {
			t.Fatalf("SaveZhipuCodingAPIKeyFile(%q) error = nil", value)
		}
	}
}

func TestZhipuPlatformAndCodingCredentialsStaySeparate(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	if err := config.SaveZhipuAPIKeyFile(paths, "platform-key"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveZhipuCodingAPIKeyFile(paths, "coding-key"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveOpenAIAPIKeyFile(paths, "other-key"); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadFiles(paths, mapLookup(map[string]string{
		config.EnvZhipuAPIKey: " ", config.EnvZhipuCodingAPIKey: " coding-env ",
		config.EnvZhipuBaseURL: " https://platform.example/v4 ", config.EnvZhipuCodingBaseURL: " https://coding.example/v4 ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.ZhipuAPIKey != "platform-key" || got.ZhipuCodingAPIKey != "coding-env" || got.OpenAIAPIKey != "other-key" {
		t.Fatal("credentials mixed or overwritten")
	}
	if got.ZhipuBaseURL != "https://platform.example/v4" || got.ZhipuCodingBaseURL != "https://coding.example/v4" {
		t.Fatal("endpoint overrides mixed")
	}
}
