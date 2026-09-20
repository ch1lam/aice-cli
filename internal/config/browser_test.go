package config_test

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestBrowserHeadedConfiguration(t *testing.T) {
	paths := testPaths(t.TempDir())
	for _, value := range []string{"true", "false"} {
		changes := map[config.Setting]string{config.SettingBrowserHeaded: value}
		if err := config.SaveSettingsFile(t.Context(), paths, changes); err != nil {
			t.Fatal(err)
		}
		var disk map[string]any
		readJSON(t, paths.GlobalSettings, &disk)
		if disk["browser_headed"] != (value == "true") {
			t.Fatal("expected JSON boolean", disk)
		}
		loaded, err := config.LoadFiles(paths, config.LoadOptions{})
		if err != nil || loaded.BrowserHeaded != (value == "true") {
			t.Fatalf("reloaded headed = %v: %v", loaded.BrowserHeaded, err)
		}
		next, err := loaded.WithSettings(map[config.Setting]string{config.SettingModel: "example"})
		if err != nil || next.BrowserHeaded != loaded.BrowserHeaded {
			t.Fatal("unrelated selection lost browser preference")
		}
	}
	opts := environmentOptions(t, map[string]string{"AICE_BROWSER_HEADED": "1"})
	loaded, err := config.LoadFiles(paths, opts)
	if err != nil || !loaded.BrowserHeaded {
		t.Fatal("environment did not override file", err)
	}
	changes := map[config.Setting]string{config.SettingBrowserHeaded: "false"}
	next, err := loaded.WithSettings(changes)
	if err != nil || next.BrowserHeaded || !next.SavedValuesOverridden(changes) {
		t.Fatal("runtime precedence", err)
	}
	if err := config.SaveSettingFile(paths, config.SettingBrowserHeaded, "sometimes"); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	t.Setenv("AICE_BROWSER_HEADED", "sometimes")
	if _, err := config.LoadFiles(paths, opts); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
