package config

import (
	"encoding/json"
	"maps"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestProviderKeysLoadWithEnvironmentPrecedence(t *testing.T) {
	paths := providerTestPaths(t)
	file := make(map[string]any)
	environment := make(map[string]string)
	for i, key := range providerKeys {
		file[key.name+"_api_key"] = "file-" + key.id
		// Odd providers are overridden by the environment; the rest keep the file key.
		if i%2 == 1 {
			environment[key.apiKeyEnv] = "environment-" + key.id
			environment[key.baseURLEnv] = " https://" + key.name + ".example/v1 "
		}
	}
	writeTestJSON(t, paths.GlobalAuth, file)
	for name, value := range environment {
		t.Setenv(name, value)
	}
	got, err := LoadFiles(paths, LoadOptions{Environment: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range providerKeys {
		wantKey, wantURL := "file-"+key.id, ""
		if i%2 == 1 {
			wantKey, wantURL = "environment-"+key.id, "https://"+key.name+".example/v1"
		}
		if got.APIKeys[key.id] != wantKey || got.BaseURLs[key.id] != wantURL {
			t.Errorf("%s = %q, %q; want %q, %q", key.id, got.APIKeys[key.id], got.BaseURLs[key.id], wantKey, wantURL)
		}
	}
}

func TestSaveAPIKeyFilePreservesOtherProviders(t *testing.T) {
	paths := providerTestPaths(t)
	want := make(map[string]string)
	for _, key := range providerKeys {
		if err := SaveAPIKeyFile(paths, key.id, " key-"+key.id+" "); err != nil {
			t.Fatalf("SaveAPIKeyFile(%s) error = %v", key.id, err)
		}
		want[key.name+"_api_key"] = "key-" + key.id
	}
	data, err := os.ReadFile(paths.GlobalAuth)
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]string
	if err := json.Unmarshal(data, &auth); err != nil || !maps.Equal(auth, want) {
		t.Fatalf("auth = %v, %v; want %v", auth, err, want)
	}
	if _, err := os.Stat(paths.GlobalSettings); !os.IsNotExist(err) {
		t.Fatalf("settings file written: %v", err)
	}
	if info, err := os.Stat(paths.GlobalAuth); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm() != 0o600) {
		t.Fatalf("auth mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestSaveAPIKeyFileRejectsInvalidValues(t *testing.T) {
	paths := providerTestPaths(t)
	for _, key := range providerKeys {
		if err := SaveAPIKeyFile(paths, key.id, "line-one\nline-two"); err == nil {
			t.Errorf("%s accepted a multiline key", key.id)
		}
		if err := SaveAPIKeyFile(paths, key.id, "  "); (err == nil) != key.keyless {
			t.Errorf("%s blank key error = %v, keyless = %v", key.id, err, key.keyless)
		}
	}
	if err := SaveAPIKeyFile(paths, "openai-codex", "key"); err == nil {
		t.Error("saved an API key for an OAuth provider")
	}
}

func TestProviderConnectionValidationNamesFieldWithoutValue(t *testing.T) {
	for _, key := range providerKeys {
		for field, value := range map[string]string{
			key.name + "_base_url": "ftp://gateway.example",
			key.name + "_api_key":  "secret\nvalue",
		} {
			paths := providerTestPaths(t)
			writeTestJSON(t, paths.GlobalSettings, map[string]any{field: value})
			_, err := LoadFiles(paths, LoadOptions{})
			if err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), value) {
				t.Errorf("%s: error = %v, want field named without value", field, err)
			}
		}
	}
}

func TestProviderConnectionSurvivesSettingChanges(t *testing.T) {
	paths := providerTestPaths(t)
	writeTestJSON(t, paths.GlobalAuth, map[string]any{"aihubmix_api_key": "saved-key"})
	writeTestJSON(t, paths.GlobalSettings, map[string]any{"aihubmix_base_url": "https://gateway.example/v1"})
	c, err := LoadFiles(paths, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.WithSettings(map[Setting]string{SettingProvider: "aihubmix", SettingModel: "gpt-6-sol"})
	if err != nil {
		t.Fatal(err)
	}
	if next.APIKeys["aihubmix"] != "saved-key" || next.BaseURLs["aihubmix"] != "https://gateway.example/v1" {
		t.Fatal("setting change lost provider connection settings")
	}
	runtime := next.WithAPIKey("aihubmix", "replaced")
	if next.APIKeys["aihubmix"] != "saved-key" || runtime.APIKeys["aihubmix"] != "replaced" {
		t.Fatal("WithAPIKey changed the source snapshot")
	}
}

func providerTestPaths(t *testing.T) Paths {
	dir := t.TempDir()
	return Paths{GlobalSettings: dir + "/settings.json", GlobalAuth: dir + "/auth.json", GlobalTrust: dir + "/trust.json", BinDir: dir + "/bin"}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
