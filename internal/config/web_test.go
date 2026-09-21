package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/trust"
)

func exampleWebSettings() map[string]any {
	return map[string]any{
		"search": map[string]any{
			"enabled":             true,
			"priority":            []string{"native", "service:exa-main"},
			"default_max_results": 8,
			"timeout":             "25s",
		},
		"services": map[string]any{
			"exa-main": map[string]any{
				"provider":   "exa",
				"api":        "exa-rest",
				"base_url":   "https://api.exa.ai",
				"credential": map[string]any{"env": "EXA_API_KEY"},
				"options":    map[string]any{"type": "auto"},
			},
		},
		"fetch": map[string]any{"enabled": true, "timeout": "30s"},
	}
}

func TestWebDefaultsWithoutConfiguration(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "m"})
	loaded, err := config.LoadFiles(paths, environmentOptions(t, map[string]string{"EXA_API_KEY": "present-but-unconfigured"}))
	if err != nil {
		t.Fatal(err)
	}
	web := loaded.Web
	if !web.SearchEnabled || !reflect.DeepEqual(web.Priority, []string{"native"}) || web.DefaultMaxResults != 8 || web.SearchTimeout != 25*time.Second {
		t.Fatalf("search defaults = %+v", web)
	}
	if !web.FetchEnabled || web.FetchTimeout != 30*time.Second || len(web.Services) != 0 {
		t.Fatalf("fetch defaults = %+v", web)
	}
	if len(loaded.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics %v", loaded.Diagnostics)
	}
}

func TestWebExampleParsesAndResolvesCredentials(t *testing.T) {
	paths := testPaths(t.TempDir())
	settings := exampleWebSettings()
	settings["services"].(map[string]any)["exa-eu"] = map[string]any{
		"provider":   "exa",
		"base_url":   "http://127.0.0.1:9000",
		"credential": map[string]any{"auth_ref": "web_services.exa-eu"},
		"enabled":    false,
	}
	settings["search"].(map[string]any)["priority"] = []string{"service:exa-eu", "native", "service:exa-main"}
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": settings})
	writeJSON(t, paths.GlobalAuth, map[string]any{"deepseek_api_key": "k", "web_services": map[string]any{"exa-eu": "eu-secret"}})
	loaded, err := config.LoadFiles(paths, environmentOptions(t, map[string]string{"EXA_API_KEY": " main-secret "}))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeepSeekAPIKey != "k" {
		t.Fatal("provider keys must survive the web namespace")
	}
	main := loaded.Web.Services["exa-main"]
	if main.Provider != "exa" || main.API != "exa-rest" || main.BaseURL != "https://api.exa.ai" || !main.SecretPresent || main.Secret != "main-secret" || !main.Enabled || string(main.Options) != `{"type":"auto"}` {
		t.Fatalf("exa-main = %+v", main)
	}
	eu := loaded.Web.Services["exa-eu"]
	if !eu.SecretPresent || eu.Secret != "eu-secret" || eu.Enabled || eu.API != "" || eu.CredentialDescription() != "auth store web_services.exa-eu" {
		t.Fatalf("exa-eu = %+v", eu)
	}
	if !reflect.DeepEqual(loaded.Web.Priority, []string{"service:exa-eu", "native", "service:exa-main"}) {
		t.Fatalf("priority = %v", loaded.Web.Priority)
	}
	if !reflect.DeepEqual(loaded.Web.ServiceIDs(), []string{"exa-eu", "exa-main"}) {
		t.Fatal(loaded.Web.ServiceIDs())
	}

	// Missing credentials keep the instance visible without inventing a key.
	missing, err := config.LoadFiles(paths, environmentOptions(t, map[string]string{"EXA_API_KEY": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Web.Services["exa-main"].SecretPresent || missing.Web.Services["exa-main"].Secret != "" {
		t.Fatal("empty environment variable counted as a credential")
	}
	// Without environment input, env references are unresolved and the
	// snapshot never reads the process environment.
	t.Setenv("EXA_API_KEY", "leak")
	isolated, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || isolated.Web.Services["exa-main"].SecretPresent {
		t.Fatalf("environment leaked into isolated load: %v", err)
	}

	// Frozen snapshots clone mutable slices.
	clone := loaded.Web.Clone()
	clone.Priority[0] = "changed"
	clone.Services["exa-main"] = config.WebService{}
	if loaded.Web.Priority[0] != "service:exa-eu" || loaded.Web.Services["exa-main"].Provider != "exa" {
		t.Fatal("clone shares state")
	}
}

func TestWebConfigurationErrorsNameTheKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		web  map[string]any
		want string
	}{
		{"unknown field", map[string]any{"search": map[string]any{"enable": true}}, "web"},
		{"missing instance", map[string]any{"search": map[string]any{"priority": []string{"service:ghost"}}}, `service "ghost" is not configured`},
		{"duplicate entry", map[string]any{"search": map[string]any{"priority": []string{"native", "native"}}}, "duplicate"},
		{"malformed entry", map[string]any{"search": map[string]any{"priority": []string{"exa"}}}, "priority[0]"},
		{"bad instance id", map[string]any{"services": map[string]any{"Exa Main": map[string]any{"provider": "exa", "credential": map[string]any{"env": "K"}}}}, "instance id"},
		{"both credentials", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa", "credential": map[string]any{"env": "K", "auth_ref": "web_services.a"}}}}, "mutually exclusive"},
		{"no credential", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa"}}}, "env or auth_ref"},
		{"foreign auth ref", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa", "credential": map[string]any{"auth_ref": "web_services.b"}}}}, "must reference this instance"},
		{"userinfo base url", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa", "base_url": "https://u:p@api.exa.ai", "credential": map[string]any{"env": "K"}}}}, "base_url"},
		{"query base url", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa", "base_url": "https://api.exa.ai/?key=x", "credential": map[string]any{"env": "K"}}}}, "base_url"},
		{"plain http base url", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa", "base_url": "http://gateway.example", "credential": map[string]any{"env": "K"}}}}, "loopback"},
		{"non object options", map[string]any{"services": map[string]any{"a": map[string]any{"provider": "exa", "credential": map[string]any{"env": "K"}, "options": []int{1}}}}, "options must be a JSON object"},
		{"bad timeout", map[string]any{"search": map[string]any{"timeout": "-5s"}}, "web.search.timeout"},
		{"bad max results", map[string]any{"search": map[string]any{"default_max_results": 50}}, "default_max_results"},
		{"bad domain", map[string]any{"search": map[string]any{"allowed_domains": []string{"https://x"}}}, "allowed_domains"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths := testPaths(t.TempDir())
			writeJSON(t, paths.GlobalSettings, map[string]any{"web": tc.web})
			_, err := config.LoadFiles(paths, config.LoadOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestProjectWebSettingsMayOnlyTighten(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": exampleWebSettings()})
	trustAll := func(config.Paths, trustDefault) (bool, error) { return true, nil }

	writeJSON(t, paths.ProjectSettings, map[string]any{"web": map[string]any{"search": map[string]any{"enabled": false}}})
	loaded, err := config.LoadFiles(paths, config.LoadOptions{TrustProject: trustAll})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Web.SearchEnabled || !loaded.Web.FetchEnabled || !reflect.DeepEqual(loaded.Web.ProjectRestricted, []string{"web.search.enabled=false"}) {
		t.Fatalf("tightening = %+v", loaded.Web)
	}
	if len(loaded.Web.Services) != 1 || loaded.Web.Services["exa-main"].BaseURL != "https://api.exa.ai" {
		t.Fatal("project tightening changed services")
	}

	for name, project := range map[string]map[string]any{
		"enable":      {"web": map[string]any{"search": map[string]any{"enabled": true}}},
		"endpoint":    {"web": map[string]any{"services": map[string]any{"exa-main": map[string]any{"provider": "exa", "base_url": "https://attacker.example", "credential": map[string]any{"env": "EXA_API_KEY"}}}}},
		"priority":    {"web": map[string]any{"search": map[string]any{"priority": []string{"service:exa-main"}}}},
		"credentials": {"web_services": map[string]any{"exa-main": "stolen"}},
	} {
		t.Run(name, func(t *testing.T) {
			writeJSON(t, paths.ProjectSettings, project)
			loaded, err := config.LoadFiles(paths, config.LoadOptions{TrustProject: trustAll})
			if err != nil {
				t.Fatal(err)
			}
			if !loaded.Web.SearchEnabled || loaded.Web.Services["exa-main"].BaseURL != "https://api.exa.ai" || !reflect.DeepEqual(loaded.Web.Priority, []string{"native", "service:exa-main"}) {
				t.Fatalf("project layer changed web configuration: %+v", loaded.Web)
			}
			if len(loaded.Diagnostics) != 1 || !strings.Contains(loaded.Diagnostics[0], "project") {
				t.Fatalf("diagnostics = %v", loaded.Diagnostics)
			}
			if loaded.Web.Services["exa-main"].SecretPresent {
				t.Fatal("project credential accepted")
			}
		})
	}

	// An untrusted project contributes nothing, including tightening.
	writeJSON(t, paths.ProjectSettings, map[string]any{"web": map[string]any{"fetch": map[string]any{"enabled": false}}})
	untrusted, err := config.LoadFiles(paths, config.LoadOptions{TrustProject: func(config.Paths, trustDefault) (bool, error) { return false, nil }})
	if err != nil || !untrusted.Web.FetchEnabled {
		t.Fatalf("untrusted project tightened fetch: %v", err)
	}
}

func TestSaveWebSettingsPatchesOnlyChangedParts(t *testing.T) {
	paths := testPaths(t.TempDir())
	settings := exampleWebSettings()
	settings["services"].(map[string]any)["exa-eu"] = map[string]any{"provider": "exa", "credential": map[string]any{"auth_ref": "web_services.exa-eu"}}
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "keep", "web": settings})

	// Add an instance and append it to the priority tail.
	enabled := true
	priority := []string{"native", "service:exa-main", "service:exa-new"}
	saved, err := config.SaveWebSettingsFile(t.Context(), paths, config.WebPatch{
		Services: map[string]*config.WebServiceSettings{"exa-new": {Provider: "exa", Credential: config.WebCredentialRef{AuthRef: "web_services.exa-new"}}},
		Priority: &priority,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Services) != 3 || saved.Search.DefaultMaxResults != 8 || saved.Fetch == nil || *saved.Fetch.Enabled != enabled {
		t.Fatalf("saved = %+v", saved)
	}
	var disk map[string]any
	readJSON(t, paths.GlobalSettings, &disk)
	if disk["model"] != "keep" {
		t.Fatal("unrelated key lost")
	}
	web := disk["web"].(map[string]any)
	if services := web["services"].(map[string]any); len(services) != 3 || services["exa-eu"] == nil {
		t.Fatalf("services = %v", services)
	}
	if got := web["search"].(map[string]any)["timeout"]; got != "25s" {
		t.Fatalf("unchanged search fields lost: %v", web["search"])
	}

	// Removing a referenced instance must fail validation and leave the file unchanged.
	if _, err := config.SaveWebSettingsFile(t.Context(), paths, config.WebPatch{Services: map[string]*config.WebServiceSettings{"exa-main": nil}}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("dangling priority accepted: %v", err)
	}
	var unchanged map[string]any
	readJSON(t, paths.GlobalSettings, &unchanged)
	if !reflect.DeepEqual(unchanged, disk) {
		t.Fatal("failed save changed the file")
	}

	// An explicit empty priority persists as [] rather than disappearing.
	empty := []string{}
	off := false
	if _, err := config.SaveWebSettingsFile(t.Context(), paths, config.WebPatch{Priority: &empty, FetchEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	readJSON(t, paths.GlobalSettings, &disk)
	web = disk["web"].(map[string]any)
	if got, ok := web["search"].(map[string]any)["priority"].([]any); !ok || len(got) != 0 {
		t.Fatalf("priority = %v", web["search"])
	}
	if web["fetch"].(map[string]any)["enabled"] != false {
		t.Fatalf("fetch = %v", web["fetch"])
	}
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || len(loaded.Web.Priority) != 0 || loaded.Web.Priority == nil || loaded.Web.FetchEnabled {
		t.Fatalf("reload = %+v %v", loaded.Web, err)
	}

	// A malformed existing web object is preserved instead of overwritten.
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": map[string]any{"search": map[string]any{"bogus": 1}}})
	if _, err := config.SaveWebSettingsFile(t.Context(), paths, config.WebPatch{FetchEnabled: &off}); err == nil || !strings.Contains(err.Error(), "left unchanged") {
		t.Fatalf("malformed target overwritten: %v", err)
	}
}

func TestSaveWebSettingsConcurrentInstances(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": map[string]any{"search": map[string]any{"priority": []string{}}}})
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			id := "svc-" + string(rune('a'+i))
			_, err := config.SaveWebSettingsFile(t.Context(), paths, config.WebPatch{Services: map[string]*config.WebServiceSettings{
				id: {Provider: "exa", Credential: config.WebCredentialRef{Env: "KEY_" + strings.ToUpper(id[4:])}},
			}})
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || len(loaded.Web.Services) != 8 {
		t.Fatalf("services = %d: %v", len(loaded.Web.Services), err)
	}
}

func TestSaveWebCredentialFile(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalAuth, map[string]any{"openai_api_key": "o"})
	if err := config.SaveWebCredentialFile(t.Context(), paths, "exa-main", "secret-1"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveWebCredentialFile(t.Context(), paths, "exa-eu", "secret-2"); err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	readJSON(t, paths.GlobalAuth, &disk)
	if disk["openai_api_key"] != "o" || !reflect.DeepEqual(disk["web_services"], map[string]any{"exa-main": "secret-1", "exa-eu": "secret-2"}) {
		t.Fatalf("auth = %v", disk)
	}
	if err := config.SaveWebCredentialFile(t.Context(), paths, "exa-main", ""); err != nil {
		t.Fatal(err)
	}
	readJSON(t, paths.GlobalAuth, &disk)
	if !reflect.DeepEqual(disk["web_services"], map[string]any{"exa-eu": "secret-2"}) {
		t.Fatalf("auth after removal = %v", disk)
	}
	if err := config.SaveWebCredentialFile(t.Context(), paths, "Bad ID", "x"); err == nil {
		t.Fatal("bad instance id accepted")
	}
	if err := config.SaveWebCredentialFile(t.Context(), paths, "exa-eu", "two\nlines"); err == nil {
		t.Fatal("multi-line secret accepted")
	}
	// Settings file must never receive the secret.
	settings := map[string]any{}
	readJSONIfExists(t, paths.GlobalSettings, &settings)
	if data, _ := json.Marshal(settings); strings.Contains(string(data), "secret") {
		t.Fatal("secret written to settings")
	}
}

func TestWithWebPublishesSnapshotWithoutRereading(t *testing.T) {
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"web": exampleWebSettings()})
	paths.ProjectSettings = filepath.Join(t.TempDir(), "p.json")
	writeJSON(t, paths.ProjectSettings, map[string]any{"web": map[string]any{"fetch": map[string]any{"enabled": false}}})
	loaded, err := config.LoadFiles(paths, config.LoadOptions{TrustProject: func(config.Paths, trustDefault) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	next, err := loaded.WithWebCredential("exa-eu", "runtime-secret").WithWeb(config.WebSettings{
		Services: map[string]config.WebServiceSettings{
			"exa-eu": {Provider: "exa", Credential: config.WebCredentialRef{AuthRef: "web_services.exa-eu"}},
		},
		Search: &config.WebSearchSettings{Priority: &[]string{"service:exa-eu"}},
		Fetch:  &config.WebFetchSettings{Enabled: boolPtr(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Web.Services["exa-eu"].Secret != "runtime-secret" || !reflect.DeepEqual(next.Web.Priority, []string{"service:exa-eu"}) {
		t.Fatalf("next = %+v", next.Web)
	}
	if next.Web.FetchEnabled {
		t.Fatal("project tightening lost after runtime change")
	}
	if loaded.Web.Services["exa-main"].Provider != "exa" || len(next.Web.Services) != 1 {
		t.Fatal("snapshots not independent")
	}
	if _, err := loaded.WithWeb(config.WebSettings{Search: &config.WebSearchSettings{Priority: &[]string{"service:missing"}}}); err == nil {
		t.Fatal("invalid runtime settings accepted")
	}
	// Ordinary setting changes carry the web snapshot along.
	changed, err := next.WithSettings(map[config.Setting]string{config.SettingModel: "m"})
	if err != nil || changed.Web.Services["exa-eu"].Secret != "runtime-secret" {
		t.Fatalf("web lost across WithSettings: %v", err)
	}
}

func boolPtr(value bool) *bool { return &value }

type trustDefault = trust.Default

func readJSONIfExists(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
