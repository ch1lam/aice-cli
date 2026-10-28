package config_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestPermissionModeSetting(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{})
	loaded, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil || loaded.PermissionMode != config.PermissionModeDefault {
		t.Fatalf("default mode = %q, %v", loaded.PermissionMode, err)
	}
	loaded, err = config.LoadFiles(paths, environmentOptions(t, map[string]string{"AICE_PERMISSION_MODE": "auto"}))
	if err != nil || loaded.PermissionMode != config.PermissionModeAuto {
		t.Fatalf("environment mode = %q, %v", loaded.PermissionMode, err)
	}
	writeJSON(t, paths.GlobalSettings, map[string]any{"permission_mode": "yolo"})
	if _, err := config.LoadFiles(paths, environmentOptions(t, nil)); err == nil || !strings.Contains(err.Error(), "permission_mode") {
		t.Fatalf("invalid mode accepted: %v", err)
	}
}

func TestAutoModeLayers(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"auto_mode": map[string]any{
		"model": "fast-model", "environment": []string{"user env"}, "block": []string{"user block"}, "allow": []string{"user allow"},
	}})
	writeJSON(t, paths.ProjectSettings, map[string]any{"auto_mode": map[string]any{
		"model": "project-model", "environment": []string{"project env"}, "block": []string{"project block"}, "allow": []string{"project allow"},
	}})
	writeJSON(t, paths.GlobalAuth, map[string]any{"auto_mode": map[string]any{"allow": []string{"auth allow"}}})
	loaded, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.AutoMode
	if got.Model != "fast-model" || !slices.Equal(got.Allow, []string{"user allow"}) ||
		!slices.Equal(got.Environment, []string{"user env", "project env"}) || !slices.Equal(got.Block, []string{"user block", "project block"}) {
		t.Fatalf("auto mode = %#v", got)
	}
	for _, want := range []string{"Ignored auto_mode in the user auth file", "Ignored auto_mode.model and auto_mode.allow in project settings"} {
		if !slices.ContainsFunc(loaded.Diagnostics, func(d string) bool { return strings.HasPrefix(d, want) }) {
			t.Errorf("missing diagnostic %q in %v", want, loaded.Diagnostics)
		}
	}
	selected, err := loaded.WithSettings(map[config.Setting]string{config.SettingModel: "x"})
	if err != nil || selected.AutoMode.Model != "fast-model" {
		t.Fatalf("runtime snapshot dropped auto mode: %#v, %v", selected.AutoMode, err)
	}
	for _, value := range []any{"x", map[string]any{"extra": 1}, map[string]any{"block": []string{" "}}, map[string]any{"model": "a b"}} {
		writeJSON(t, paths.GlobalSettings, map[string]any{"auto_mode": value})
		if _, err := config.LoadFiles(paths, environmentOptions(t, nil)); err == nil {
			t.Errorf("auto_mode %#v accepted", value)
		}
	}
}
