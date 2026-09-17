package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ch1lam/aice-cli/internal/config"
)

func TestLayeredConfigurationAndFrozenRuntime(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), ".aice", "settings.json")
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "user", "no_dep_install": true})
	writeJSON(t, paths.ProjectSettings, map[string]any{"model": "project", "no_dep_install": false})
	options := environmentOptions(t, map[string]string{config.EnvModel: "environment"})
	command := &cobra.Command{}
	command.Flags().String("model", "flag-default", "")
	options.BindFlags = func(v *viper.Viper) error { return v.BindPFlag("model", command.Flags().Lookup("model")) }
	loaded, err := config.LoadFiles(paths, options)
	if err != nil || loaded.Model != "environment" || loaded.NoDepInstall {
		t.Fatalf("environment/project precedence = %q/%v: %v", loaded.Model, loaded.NoDepInstall, err)
	}
	if err := command.Flags().Set("model", "flag"); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.LoadFiles(paths, options)
	if err != nil || loaded.Model != "flag" {
		t.Fatalf("flag precedence = %q: %v", loaded.Model, err)
	}
	changes := map[config.Setting]string{config.SettingModel: "interactive"}
	selected, err := loaded.WithSettings(changes)
	if err != nil || selected.Model != "interactive" || loaded.Model != "flag" {
		t.Fatalf("runtime override = %q, original = %q: %v", selected.Model, loaded.Model, err)
	}
	if err := config.SaveSettingsFile(t.Context(), paths, changes); err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	readJSON(t, paths.GlobalSettings, &disk)
	if disk["model"] != "interactive" || len(disk) != 2 {
		t.Fatalf("persisted merged values: %#v", disk)
	}
	restarted, err := config.LoadFiles(paths, options)
	if err != nil || restarted.Model != "flag" {
		t.Fatalf("restart ignored priority: %q: %v", restarted.Model, err)
	}
	// A peer and the process environment change after loading. A local edit
	// must derive exclusively from this instance's snapshot.
	writeJSON(t, paths.ProjectSettings, map[string]any{"model": "peer", "custom_base_url": "https://peer.example", "no_dep_install": true})
	t.Setenv(config.EnvDeepSeekAPIKey, "later-key")
	next, err := selected.WithSettings(map[config.Setting]string{config.SettingThinking: "low"})
	if err != nil || next.Model != "interactive" || next.CustomBaseURL != "" || next.DeepSeekAPIKey != "" || next.NoDepInstall {
		t.Fatalf("runtime edit reloaded another source: %v", err)
	}
}

func TestFinalValidationAndUnparseableLayers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, low, high string
		wantErr         bool
	}{
		{"masked type", `{"model":3,"thinking":"extreme","no_update_check":"invalid"}`, `{"model":"valid","thinking":"high","no_update_check":false}`, false},
		{"masked object", `{"model":{"bad":"shape"},"context_windows":{}}`, `{"model":"valid","context_windows":[]}`, false},
		{"final empty object", `{"model":"valid"}`, `{"model":{}}`, true},
		{"final object", `{"model":"valid"}`, `{"model":{"bad":"shape"}}`, true},
		{"final invalid", `{"thinking":"high"}`, `{"thinking":"extreme"}`, true},
		{"broken low", `{"model":`, `{"model":"valid"}`, false},
		{"broken high", `{"model":"valid"}`, `{"model":"partial",`, false},
		{"broken both", `oops`, `[]`, false},
		{"fractional context", `{}`, `{"context_windows":[{"provider":"custom","model":"M","tokens":1.5}]}`, true},
		{"masked context", `{"context_windows":"invalid"}`, `{"context_windows":[{"provider":"custom","model":"M","tokens":123}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := testPaths(t.TempDir())
			paths.ProjectSettings = filepath.Join(filepath.Dir(paths.GlobalSettings), "project.json")
			if err := os.MkdirAll(filepath.Dir(paths.GlobalSettings), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.GlobalSettings, []byte(tc.low), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.ProjectSettings, []byte(tc.high), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := config.LoadFiles(paths, config.LoadOptions{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}
			if tc.name == "broken high" && got.Model != "valid" {
				t.Fatal("partially parsed layer leaked into result")
			}
			if strings.HasPrefix(tc.name, "broken") && len(got.Diagnostics) == 0 {
				t.Fatal("missing non-blocking diagnostic")
			}
		})
	}
}

func TestAllConfigurationSourcesIncludingEnvironmentOnlyValues(t *testing.T) {
	paths := testPaths(t.TempDir())
	paths.ProjectSettings = filepath.Join(t.TempDir(), "project.json")
	writeJSON(t, paths.GlobalAuth, map[string]any{"openai_api_key": 123})
	writeJSON(t, paths.GlobalSettings, map[string]any{"deepseek_base_url": "invalid", "no_dep_install": false})
	writeJSON(t, paths.ProjectSettings, map[string]any{"openai_api_key": "project-key", "deepseek_base_url": "https://project.example"})
	opts := environmentOptions(t, map[string]string{
		config.EnvOpenAIAPIKey: "environment-key", "AICE_NO_DEP_INSTALL": "1", "AICE_NO_UPDATE_CHECK": "false",
		"AICE_DEFAULT_PROJECT_TRUST": "never",
		"AICE_CONTEXT_WINDOWS":       `[{"provider":"custom","model":"Org/Model.v1","tokens":9007199254740993}]`,
	})
	got, err := config.LoadFiles(paths, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.OpenAIAPIKey != "environment-key" || got.DeepSeekBaseURL != "https://project.example" || !got.NoDepInstall || got.NoUpdateCheck || got.DefaultProjectTrust != "never" || got.ContextWindows["custom/Org/Model.v1"] != 9007199254740993 {
		t.Fatal("unified source resolution or exact identifiers/integers failed")
	}
	t.Setenv("AICE_NO_DEP_INSTALL", "sometimes")
	if _, err := config.LoadFiles(paths, opts); err == nil {
		t.Fatal("invalid boolean silently converted")
	}
	t.Setenv("AICE_NO_DEP_INSTALL", "0")
	t.Setenv(config.EnvOpenAIAPIKey, "")
	got, err = config.LoadFiles(paths, opts)
	if err != nil || got.NoDepInstall || got.OpenAIAPIKey != "project-key" {
		t.Fatalf("false/empty environment semantics: %v", err)
	}
}

func TestImmediatePersistenceAndIndependentInstances(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"model": "X", "custom_base_url": "https://old.example"})
	a, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	changes := map[config.Setting]string{config.SettingModel: "Y"}
	if err := config.SaveSettingsFile(t.Context(), paths, changes); err != nil {
		t.Fatal(err)
	}
	a, err = a.WithSettings(changes)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || a.Model != "Y" || b.Model != "X" || c.Model != "Y" {
		t.Fatalf("instance isolation failed: %v", err)
	}
	if err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{config.SettingCustomBaseURL: "https://new.example"}); err != nil {
		t.Fatal(err)
	}
	c, err = config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || c.Model != "Y" || c.CustomBaseURL != "https://new.example" {
		t.Fatal("unrelated saved selection was overwritten")
	}
}

func TestSavePreservesDamagedSourcesAndShadowedValues(t *testing.T) {
	t.Parallel()
	paths := testPaths(t.TempDir())
	writeJSON(t, paths.GlobalSettings, map[string]any{"thinking": "invalid", "unrelated": "keep"})
	if err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{config.SettingModel: "chosen"}); err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	readJSON(t, paths.GlobalSettings, &disk)
	if !reflect.DeepEqual(disk, map[string]any{"thinking": "invalid", "unrelated": "keep", "model": "chosen"}) {
		t.Fatalf("unrelated data changed: %#v", disk)
	}
	broken := []byte(`{"model":`)
	if err := os.WriteFile(paths.GlobalSettings, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{config.SettingModel: "other"}); err == nil {
		t.Fatal("silently overwrote damaged file")
	}
	after, err := os.ReadFile(paths.GlobalSettings)
	if err != nil || string(after) != string(broken) {
		t.Fatalf("damaged source changed: %v", err)
	}
	lock := paths.GlobalSettings + ".lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = config.SaveSettingsFile(ctx, paths, map[config.Setting]string{config.SettingModel: "other"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait = %v", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("waiting writer removed owner's lock")
	}
}

func TestConcurrentConfigWriterProcess(t *testing.T) {
	root := os.Getenv("AICE_TEST_CONFIG_WRITER_ROOT")
	if root == "" {
		return
	}
	paths := testPaths(root)
	key := config.Setting(os.Getenv("AICE_TEST_CONFIG_WRITER_KEY"))
	value := os.Getenv("AICE_TEST_CONFIG_WRITER_VALUE")
	for range 10 {
		if err := config.SaveSettingsFile(t.Context(), paths, map[config.Setting]string{key: value}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentProcessesPatchWithoutLostUpdates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	paths := testPaths(root)
	writeJSON(t, paths.GlobalSettings, map[string]any{"untouched": "kept"})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	values := map[config.Setting]string{config.SettingProvider: "custom", config.SettingModel: "Org/Model.v1", config.SettingThinking: "high", config.SettingCustomBaseURL: "https://local.example"}
	var wg sync.WaitGroup
	for key, value := range values {
		wg.Go(func() {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentConfigWriterProcess$")
			cmd.Env = append(os.Environ(), "AICE_TEST_CONFIG_WRITER_ROOT="+root, "AICE_TEST_CONFIG_WRITER_KEY="+string(key), "AICE_TEST_CONFIG_WRITER_VALUE="+value)
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("writer failed: %v\n%s", err, data)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	defer func() {
		// Fatal reader errors must still stop and join every writer before the
		// test returns and TempDir cleanup starts.
		cancel()
		<-done
	}()
	for {
		select {
		case <-done:
			var disk map[string]string
			readJSON(t, paths.GlobalSettings, &disk)
			if disk["untouched"] != "kept" {
				t.Fatal("lost untouched field")
			}
			for key, want := range values {
				if disk[string(key)] != want {
					t.Fatalf("lost %s update", key)
				}
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
			data, err := os.ReadFile(paths.GlobalSettings)
			// Windows can reject opening the file while a rename holds delete
			// access. Retry on the next poll within the existing test deadline.
			const sharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
			if runtime.GOOS == "windows" && errors.Is(err, sharingViolation) {
				continue
			}
			if err != nil {
				t.Fatalf("reader failed: %v", err)
			}
			if !json.Valid(data) {
				t.Fatal("reader saw partial JSON file")
			}
		}
	}
}
