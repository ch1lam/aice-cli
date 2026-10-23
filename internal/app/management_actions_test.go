package app

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestWebManagementActions(t *testing.T) {
	for _, scenario := range []string{"toggle", "invalid", "cancel", "credential saved preference failed"} {
		t.Run(scenario, func(t *testing.T) {
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
			wantRevision, wantResources := uint64(0), uint64(0)
			if scenario == "toggle" {
				wantRevision, wantResources = 1, 1
			}
			if scenario == "credential saved preference failed" {
				wantRevision = 1
			}
			result, err := s.RunSettingsAction(t.Context(), 0, request)
			output := result.Output
			if result.Revision != wantRevision || result.Committed || result.Applied || len(result.Warnings) != 0 {
				t.Fatalf("Settings result = %+v", result)
			}
			if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantResources || s.lifecycle.changing {
				t.Error("Web revisions do not reflect committed/published effects")
			}
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

func TestBrowserManagementActions(t *testing.T) {
	for _, scenario := range []string{"toggle", "save failure", "connected then cancel tab"} {
		t.Run(scenario, func(t *testing.T) {
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
			wantRevision := uint64(1)
			if scenario == "save failure" {
				wantRevision = 0
			}
			result, err := s.RunSettingsAction(t.Context(), 0, request)
			output := result.Output
			if result.Revision != wantRevision || result.Committed || result.Applied || len(result.Warnings) != 0 {
				t.Fatalf("Settings action result = %+v", result)
			}
			if s.lifecycle.revision != wantRevision || s.lifecycle.resourceRevision != wantRevision || s.lifecycle.changing {
				t.Fatal("browser effects did not determine completion revisions")
			}
			switch scenario {
			case "toggle":
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
			if scenario != "toggle" {
				if _, statErr := os.Stat(s.configuration.Paths.GlobalSettings); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("unsaved action wrote preferences: %v", statErr)
				}
			}
		})
	}
}

// Slash entries for Settings-owned domains delegate to the same action without
// a draft revision, so the effect matrices run once through RunSettingsAction.
// This table proves the delegation: slash ignores the draft revision and folds
// warnings into its output, with identical lifecycle effects.
func TestSlashDelegatesToSettingsActions(t *testing.T) {
	for _, test := range []struct {
		name    string
		session func(*testing.T) *interactiveSession
		request func() interaction.CommandRequest
	}{
		{
			name: "web",
			session: func(t *testing.T) *interactiveSession {
				return webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), nil)
			},
			request: func() interaction.CommandRequest {
				return interaction.CommandRequest{Name: "web", Arguments: "fetch"}
			},
		},
		{
			name: "browser",
			session: func(t *testing.T) *interactiveSession {
				s, _ := browserTestSession(t)
				t.Cleanup(func() { _ = closeBrowser(context.Background(), s.browser) })
				s.configuration.Paths = authTestPaths(t)
				s.application = &application{dependencies: dependencies{saveSettings: config.SaveSettingsFile}}
				return s
			},
			request: func() interaction.CommandRequest {
				return interaction.CommandRequest{Name: "browser", Arguments: "headed"}
			},
		},
		{
			name: "login",
			session: func(t *testing.T) *interactiveSession {
				return &interactiveSession{
					configuration: config.Config{Provider: "deepseek", Paths: authTestPaths(t)},
					model:         deepseek.DefaultModel(), providers: defaultProviders(),
					application: &application{dependencies: dependencies{
						providers:    defaultProviders(),
						newModel:     func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
						saveSettings: config.SaveSettingsFile,
						codexInteractiveLogin: func(context.Context, bool, codex.LoginInteraction) (config.CodexCredentials, error) {
							return config.CodexCredentials{AccessToken: "synthetic-token", RefreshToken: "synthetic-refresh", AccountID: "fixture", ExpiresAt: time.Now().Add(time.Hour)}, nil
						},
					}},
				}
			},
			request: func() interaction.CommandRequest {
				return interaction.CommandRequest{Name: "login", Arguments: "openai-codex", LoginMethod: "browser", Auth: &interaction.AuthInteraction{Notify: func(context.Context, interaction.AuthPrompt) error { return nil }}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			const draft = 3
			viaSettings := test.session(t)
			viaSettings.lifecycle.revision = draft
			result, err := viaSettings.RunSettingsAction(t.Context(), draft, test.request())
			if err != nil {
				t.Fatalf("RunSettingsAction() error = %v", err)
			}
			viaSlash := test.session(t)
			// Slash has no draft, so it never checks the observed revision.
			viaSlash.lifecycle.revision = draft
			output, err := viaSlash.RunSlashCommand(t.Context(), test.request())
			if err != nil {
				t.Fatalf("RunSlashCommand() error = %v", err)
			}
			if want := appendWarnings(result.Output, result.Warnings); output != want || output == "" {
				t.Fatalf("slash output = %q, want %q", output, want)
			}
			if viaSlash.lifecycle.revision != draft+1 || viaSlash.lifecycle.changing ||
				viaSlash.lifecycle.revision != viaSettings.lifecycle.revision ||
				viaSlash.lifecycle.resourceRevision != viaSettings.lifecycle.resourceRevision {
				t.Fatalf("slash revisions = %d/%d changing=%v, settings revisions = %d/%d",
					viaSlash.lifecycle.revision, viaSlash.lifecycle.resourceRevision, viaSlash.lifecycle.changing,
					viaSettings.lifecycle.revision, viaSettings.lifecycle.resourceRevision)
			}
		})
	}
}
