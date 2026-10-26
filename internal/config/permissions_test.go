package config_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestPermissionRulesConcatenateTrustedLayers(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"permissions": map[string]any{"allow": []string{"bash(git push *)"}, "deny": []string{"bash(curl *)"}}})
	writeJSON(t, paths.GlobalAuth, map[string]any{"permissions": map[string]any{"allow": []string{"bash(rm *)"}}})
	writeJSON(t, paths.ProjectSettings, map[string]any{"permissions": map[string]any{"allow": []string{"bash(make *)"}, "ask": []string{"write(./docs/**)"}}})
	loaded, err := config.LoadFiles(paths, environmentOptions(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := config.PermissionRules{Allow: []string{"bash(git push *)", "bash(make *)"}, Ask: []string{"write(./docs/**)"}, Deny: []string{"bash(curl *)"}}
	if !slices.Equal(loaded.Permissions.Allow, want.Allow) || !slices.Equal(loaded.Permissions.Ask, want.Ask) || !slices.Equal(loaded.Permissions.Deny, want.Deny) {
		t.Fatalf("permissions = %#v, want %#v", loaded.Permissions, want)
	}
	if !slices.ContainsFunc(loaded.Diagnostics, func(d string) bool { return d == "Ignored permissions in the user auth file: rules belong in settings" }) {
		t.Fatalf("auth-file rules not reported: %v", loaded.Diagnostics)
	}
	selected, err := loaded.WithSettings(map[config.Setting]string{config.SettingModel: "x"})
	if err != nil || !slices.Equal(selected.Permissions.Allow, want.Allow) {
		t.Fatalf("runtime snapshot dropped permissions: %#v, %v", selected.Permissions, err)
	}

	untrusted := environmentOptions(t, nil)
	untrusted.TrustProject = func(config.Paths, trustDefault) (bool, error) { return false, nil }
	loaded, err = config.LoadFiles(paths, untrusted)
	if err != nil || slices.Contains(loaded.Permissions.Allow, "bash(make *)") {
		t.Fatalf("untrusted project rules loaded: %#v, %v", loaded.Permissions, err)
	}
}

func TestPermissionRulesRejectMalformedLists(t *testing.T) {
	for _, value := range []any{"bash(x)", map[string]any{"allow": "bash(x)"}, map[string]any{"allow": []string{" "}}, map[string]any{"maybe": []string{"bash(x)"}}} {
		paths := testPaths(t.TempDir())
		writeJSON(t, paths.GlobalSettings, map[string]any{"permissions": value})
		if _, err := config.LoadFiles(paths, environmentOptions(t, nil)); err == nil {
			t.Errorf("permissions %#v accepted", value)
		}
	}
}

func TestSavePermissionRuleAppendsOnce(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "m", "permissions": map[string]any{"deny": []string{"bash(curl *)"}}})
	for range 2 {
		if err := config.SavePermissionRule(t.Context(), paths, "allow", "bash(git push *)"); err != nil {
			t.Fatal(err)
		}
	}
	var disk struct {
		Model       string                 `json:"model"`
		Permissions config.PermissionRules `json:"permissions"`
	}
	readJSON(t, paths.GlobalSettings, &disk)
	if disk.Model != "m" || !slices.Equal(disk.Permissions.Allow, []string{"bash(git push *)"}) || !slices.Equal(disk.Permissions.Deny, []string{"bash(curl *)"}) {
		t.Fatalf("saved settings = %#v", disk)
	}
	if err := config.SavePermissionRule(t.Context(), paths, "maybe", "bash(x)"); err == nil {
		t.Fatal("unknown decision saved")
	}
}
