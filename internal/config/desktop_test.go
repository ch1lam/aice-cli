package config_test

import (
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/trust"
	"github.com/spf13/viper"
)

func TestDesktopOnlyAcceptsUserPreferences(t *testing.T) {
	t.Setenv("AICE_DESKTOP_ENABLED", "true")
	t.Setenv("AICE_DESKTOP_CONTROL_MODE", "foreground_allowed")
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), "project.json")
	writeJSON(t, paths.GlobalAuth, map[string]any{"desktop_enabled": true, "desktop_control_mode": "foreground_allowed"})
	writeJSON(t, paths.ProjectSettings, map[string]any{"DESKTOP_ENABLED": true, "Desktop_Control_Mode": map[string]any{"invalid": "ignored"}, "max_turns": 3})
	options := config.LoadOptions{Environment: true, TrustProject: func(config.Paths, trust.Default) (bool, error) { return true, nil }, BindFlags: func(v *viper.Viper) error {
		v.Set("desktop_enabled", true)
		v.Set("desktop_control_mode", "foreground_allowed")
		return nil
	}}
	c, err := config.LoadFiles(paths, options)
	if err != nil {
		t.Fatal(err)
	}
	if c.DesktopEnabled || c.DesktopControlMode != config.DesktopBackgroundOnly || c.MaxTurns != 3 {
		t.Fatal("project/auth/invocation changed desktop authority")
	}
	for _, id := range []config.Setting{config.SettingDesktopEnabled, config.SettingDesktopControlMode} {
		state, err := c.SettingState(id)
		if err != nil || state.Source.Kind != "default" || state.Saved != nil {
			t.Fatalf("state=%+v err=%v", state, err)
		}
	}
	writeJSON(t, paths.GlobalSettings, map[string]any{"desktop_enabled": true, "desktop_control_mode": "foreground_allowed"})
	c, err = config.LoadFiles(paths, options)
	if err != nil || !c.DesktopEnabled || c.DesktopControlMode != config.DesktopForegroundAllowed {
		t.Fatal("user choice not accepted", err)
	}
	state, err := c.SettingState(config.SettingDesktopEnabled)
	if err != nil || state.Source.Kind != "user-settings" || state.Inherited.Bool {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	next, err := c.WithPatch(settingPatch(t, config.SettingDesktopEnabled, "false"))
	if err != nil || next.DesktopEnabled {
		t.Fatal(err)
	}
	next, err = next.WithPatch(config.SettingsPatch{Changes: []config.SettingChange{{ID: config.SettingDesktopEnabled, Unset: true}, {ID: config.SettingDesktopControlMode, Unset: true}}})
	if err != nil || next.DesktopEnabled || next.DesktopControlMode != config.DesktopBackgroundOnly {
		t.Fatal("unset imported forbidden layer", err)
	}
}

func TestDesktopPatchPersistenceAndValidation(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "original"})
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	patch := settingPatch(t, config.SettingDesktopEnabled, "true")
	next, err := c.WithPatch(patch)
	if err != nil || !next.DesktopEnabled {
		t.Fatal(err)
	}
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "peer", "unknown": "preserve"})
	saved, err := config.SaveSettingsPatch(t.Context(), paths, patch)
	if err != nil || !saved.Committed {
		t.Fatal(saved, err)
	}
	var disk map[string]any
	readJSON(t, paths.GlobalSettings, &disk)
	if disk["model"] != "peer" || disk["unknown"] != "preserve" || disk["desktop_enabled"] != true || next.Model != "original" {
		t.Fatal("lost peer/local isolation")
	}
	state, err := next.SettingState(config.SettingDesktopEnabled)
	if err != nil || state.Source.Kind != "runtime" || state.Saved == nil || !state.Saved.Bool {
		t.Fatal(state, err)
	}
	for _, value := range []string{"unrestricted", "foreground", "invalid"} {
		patch := settingPatch(t, config.SettingDesktopControlMode, value)
		if _, err := c.WithPatch(patch); err == nil {
			t.Fatal("invalid mode accepted")
		}
		if saved, err := config.SaveSettingsPatch(t.Context(), paths, patch); err == nil || saved.Committed {
			t.Fatal("invalid mode persisted")
		}
	}
}
