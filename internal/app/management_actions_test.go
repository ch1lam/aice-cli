package app

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// These cases preserve the existing entry-point revision difference: Settings
// invalidates drafts after failed mutation attempts; slash only does on success.
func managementAction(t *testing.T, s *interactiveSession, entry string, request interaction.CommandRequest) (string, error) {
	t.Helper()
	var output string
	var err error
	if entry == "slash" {
		output, err = s.RunSlashCommand(t.Context(), request)
	} else {
		result, actionErr := s.RunSettingsAction(t.Context(), 0, request)
		output, err = result.Output, actionErr
		if result.Revision != 1 || result.Committed || result.Applied || len(result.Warnings) != 0 {
			t.Fatalf("Settings action result = %+v", result)
		}
	}
	wantRevision := uint64(1)
	if entry == "slash" && err != nil {
		wantRevision = 0
	}
	if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantRevision || s.lifecycle.changing {
		t.Fatal("action changed revision or reservation semantics")
	}
	return output, err
}

func TestWebManagementActionEntryPoints(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		for _, scenario := range []string{"toggle", "invalid", "cancel", "credential saved preference failed"} {
			t.Run(entry+"/"+scenario, func(t *testing.T) {
				search := &fakeWireBackend{}
				backends := testWebBackends(search, &fakeFetch{})
				backends.search["exa"] = fakeSearchFactory(search, "exa-rest")
				s := webCommandSession(t, backends, nil)
				before := s.settingsSnapshot()
				_, auth := newScriptedUI()
				request := interaction.CommandRequest{Name: "web", Arguments: " fetch ", Auth: auth}
				switch scenario {
				case "invalid":
					request.Arguments = "missing"
				case "cancel":
					request.Arguments = "add"
					request.Auth.Notify = func(context.Context, interaction.AuthPrompt) error { return context.Canceled }
				case "credential saved preference failed":
					request.Arguments = "add"
					_, request.Auth = newScriptedUI("key", "synthetic-web-key")
					s.application.dependencies.saveWebSettings = func(context.Context, config.Paths, config.WebPatch) (config.WebSettings, error) {
						return config.WebSettings{}, errors.New("synthetic preference failure")
					}
				}
				output, err := managementAction(t, s, entry, request)
				if scenario == "toggle" {
					loaded, loadErr := config.LoadFiles(s.configuration.Paths, config.LoadOptions{})
					if err != nil || loadErr != nil || loaded.Web.FetchEnabled || s.configuration.Web.FetchEnabled || slicesContains(toolNames(s.tools), "web_fetch") || s.loop == before.loop || !strings.Contains(output, "Web fetch: off (saved)") {
						t.Fatalf("toggle did not save and publish: %q, %v, %v", output, err, loadErr)
					}
				} else {
					if err == nil || output != "" || !reflect.DeepEqual(s.configuration.Web, before.configuration.Web) || !reflect.DeepEqual(toolNames(s.tools), toolNames(before.tools)) || s.loop != before.loop || s.systemPrompt != before.systemPrompt {
						t.Fatalf("failed action changed published resources: %q, %v", output, err)
					}
					if _, statErr := os.Stat(s.configuration.Paths.GlobalSettings); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("failed action saved preferences: %v", statErr)
					}
					if scenario == "cancel" && !errors.Is(err, context.Canceled) || scenario == "invalid" && !strings.Contains(err.Error(), "unknown web action") {
						t.Fatalf("unexpected failure: %v", err)
					}
					if scenario == "credential saved preference failed" {
						data, readErr := os.ReadFile(s.configuration.Paths.GlobalAuth)
						if readErr != nil || !strings.Contains(string(data), "synthetic-web-key") || !strings.Contains(err.Error(), "credential saved to") {
							t.Fatalf("credential-only success lost: %v, %v", err, readErr)
						}
					}
				}
				if search.calls.Load() != 0 {
					t.Fatal("management action called search API")
				}
			})
		}
	}
}

func TestBrowserManagementActionEntryPoints(t *testing.T) {
	for _, entry := range []string{"slash", "settings"} {
		for _, scenario := range []string{"toggle", "saved with cleanup warning", "save failure", "connected then cancel tab"} {
			t.Run(entry+"/"+scenario, func(t *testing.T) {
				s, _ := browserTestSession(t)
				t.Cleanup(func() { _ = closeBrowser(context.Background(), s.browser) })
				s.configuration.Paths = authTestPaths(t)
				s.application = &application{dependencies: dependencies{saveSettings: config.SaveSettingsFile}}
				request := interaction.CommandRequest{Name: "browser", Arguments: " headed "}
				if scenario == "save failure" {
					s.application.dependencies.saveSettings = func(context.Context, config.Paths, map[config.Setting]string) error {
						return errors.New("synthetic preference failure")
					}
				}
				if scenario == "saved with cleanup warning" {
					s.application.dependencies.saveSettings = func(ctx context.Context, paths config.Paths, changes map[config.Setting]string) error {
						if err := config.SaveSettingsFile(ctx, paths, changes); err != nil {
							return err
						}
						return &config.CommittedError{Warning: errors.New("synthetic cleanup warning")}
					}
				}
				if scenario == "connected then cancel tab" {
					if _, _, err := s.browser.Connect(t.Context(), browser.Target{Endpoint: "9111"}); err != nil {
						t.Fatal(err)
					}
					if err := applyBrowserEnvironment(s.browser); err != nil {
						t.Fatal(err)
					}
					request.Arguments = " connect "
					input := make(chan string, 1)
					request.Auth = &interaction.AuthInteraction{Input: input, Notify: func(_ context.Context, prompt interaction.AuthPrompt) error {
						if prompt.Menu != nil {
							return context.Canceled
						}
						input <- "9222"
						return nil
					}}
				}
				beforeName := s.browser.Name()
				var output string
				var err error
				var warnings []string
				wantRevision := uint64(1)
				if scenario == "save failure" {
					wantRevision = 0
				}
				if entry == "slash" {
					output, err = s.RunSlashCommand(t.Context(), request)
				} else {
					result, actionErr := s.RunSettingsAction(t.Context(), 0, request)
					output, err = result.Output, actionErr
					warnings = result.Warnings
					if result.Revision != wantRevision || result.Committed || result.Applied {
						t.Fatalf("Settings action result = %+v", result)
					}
				}
				if scenario == "saved with cleanup warning" {
					if !strings.Contains(output+strings.Join(warnings, "\n"), "synthetic cleanup warning") {
						t.Fatal("committed cleanup warning lost")
					}
				} else if len(warnings) != 0 {
					t.Fatalf("unexpected warnings: %v", warnings)
				}
				if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantRevision || s.lifecycle.changing {
					t.Fatal("browser effects did not determine completion revisions")
				}
				switch scenario {
				case "toggle", "saved with cleanup warning":
					loaded, loadErr := config.LoadFiles(s.configuration.Paths, config.LoadOptions{})
					if err != nil || loadErr != nil || !loaded.BrowserHeaded || !s.configuration.BrowserHeaded || !s.browser.Headed() || os.Getenv("AGENT_BROWSER_HEADED") != "true" || s.browser.Name() != beforeName || !strings.Contains(output, "Show window: on (saved)") {
						t.Fatalf("toggle did not save and publish: %q, %v, %v", output, err, loadErr)
					}
				case "save failure":
					if err == nil || output != "" || s.configuration.BrowserHeaded || s.browser.Headed() || os.Getenv("AGENT_BROWSER_HEADED") != "false" || s.browser.Name() != beforeName {
						t.Fatalf("failed save changed browser state: %q, %v", output, err)
					}
				case "connected then cancel tab":
					if !errors.Is(err, context.Canceled) || output != "" || s.browser.Target() != (browser.Target{Endpoint: "9222"}) || s.browser.Name() == beforeName || !s.browser.HasSidecar() || os.Getenv("AGENT_BROWSER_CDP") != "9222" || os.Getenv("AGENT_BROWSER_SESSION") != s.browser.Name() {
						t.Fatalf("cancellation lost completed connection effects: %q, %v", output, err)
					}
				}
				if scenario != "toggle" && scenario != "saved with cleanup warning" {
					if _, statErr := os.Stat(s.configuration.Paths.GlobalSettings); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("unsaved action wrote preferences: %v", statErr)
					}
				}
			})
		}
	}
}
