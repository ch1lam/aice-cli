package config_test

import (
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestLoadFilesResolvesAiHubMixCredentials(t *testing.T) {

	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{
		"aihubmix_api_key": "file-key",
	})

	values := map[string]string{
		config.EnvAiHubMixAPIKey:  "environment-key",
		config.EnvAiHubMixBaseURL: " https://aihubmix.example/v1 ",
	}
	got, err := config.LoadFiles(paths, environmentOptions(t, values))
	if err != nil {
		t.Fatalf("LoadFiles() error = %v", err)
	}
	if got.AiHubMixAPIKey != "environment-key" {
		t.Errorf("AiHubMixAPIKey = %q, want environment-key", got.AiHubMixAPIKey)
	}
	if got.AiHubMixBaseURL != "https://aihubmix.example/v1" {
		t.Errorf(
			"AiHubMixBaseURL = %q, want trimmed custom URL",
			got.AiHubMixBaseURL,
		)
	}
}

func TestSaveAiHubMixAPIKeyFilePreservesOtherProviderKeys(t *testing.T) {

	paths := testPaths(t.TempDir())
	if err := config.SaveDeepSeekAPIKeyFile(paths, "deepseek-key"); err != nil {
		t.Fatalf("SaveDeepSeekAPIKeyFile() error = %v", err)
	}
	if err := config.SaveOpenCodeAPIKeyFile(paths, "opencode-key"); err != nil {
		t.Fatalf("SaveOpenCodeAPIKeyFile() error = %v", err)
	}
	if err := config.SaveAiHubMixAPIKeyFile(paths, " aihubmix-key "); err != nil {
		t.Fatalf("SaveAiHubMixAPIKeyFile() error = %v", err)
	}

	var auth map[string]string
	readJSON(t, paths.GlobalAuth, &auth)
	want := map[string]string{
		"deepseek_api_key": "deepseek-key",
		"opencode_api_key": "opencode-key",
		"aihubmix_api_key": "aihubmix-key",
	}
	if !reflect.DeepEqual(auth, want) {
		t.Errorf("auth = %#v, want %#v", auth, want)
	}
}

func TestSaveAiHubMixAPIKeyFileRejectsInvalidValues(t *testing.T) {

	paths := testPaths(t.TempDir())
	for _, value := range []string{"", "  ", "line-one\nline-two"} {
		err := config.SaveAiHubMixAPIKeyFile(paths, value)
		if err == nil {
			t.Fatalf("SaveAiHubMixAPIKeyFile(%q) error = nil", value)
		}
	}
}

func TestAiHubMixSnapshotRetainsConnection(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{"aihubmix_api_key": "saved-key"})
	writeJSON(t, paths.GlobalSettings, map[string]any{"aihubmix_base_url": "https://gateway.example/v1"})
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.WithSettings(map[config.Setting]string{config.SettingProvider: "aihubmix", config.SettingModel: "gpt-6-sol"})
	if err != nil {
		t.Fatal(err)
	}
	if next.AiHubMixAPIKey != "saved-key" || next.AiHubMixBaseURL != "https://gateway.example/v1" {
		t.Fatal("interactive selection lost AiHubMix connection settings")
	}
}

func TestAiHubMixInvalidConnectionSettings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, key, value string }{
		{"endpoint scheme", "aihubmix_base_url", "ftp://gateway.example"},
		{"endpoint host", "aihubmix_base_url", "https://"},
		{"multiline credential", "aihubmix_api_key", "secret\nvalue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := testPaths(t.TempDir())
			writeJSON(t, paths.GlobalSettings, map[string]any{tc.key: tc.value})
			if _, err := config.LoadFiles(paths, config.LoadOptions{}); err == nil {
				t.Fatal("accepted invalid connection setting")
			}
		})
	}
}
