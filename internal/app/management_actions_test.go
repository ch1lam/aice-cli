package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/browser"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/provider/codex"
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

// The browser preset only writes and approves the ordinary user:browser
// service; each choice is a definition change through the generic MCP path.
func TestBrowserPresetActions(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agent-browser")
	paths := runMCPSettingsConfigurationFixture(t, `"mcp":{"servers":{}}`, func(ctx context.Context, s *interactiveSession) {
		act := func(action string, input ...string) interaction.SettingsActionResult {
			t.Helper()
			values := make(chan string, len(input))
			for _, value := range input {
				values <- value
			}
			revision, _ := s.settingsStatus()
			auth := &interaction.AuthInteraction{Input: values, Notify: func(context.Context, interaction.AuthPrompt) error { return nil }}
			result, err := s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "browser", Arguments: action, Auth: auth})
			if err != nil {
				t.Fatal(action, err)
			}
			return result
		}
		choices := func() (bool, browser.Target) {
			t.Helper()
			server, exists := s.configuration.MCP.Servers[browserServiceKey]
			if !exists || !server.Enabled || s.configuration.MCP.ConnectionDecision(browserServiceKey) != config.MCPConnectionAllow || server.Settings.Command != bin {
				t.Fatalf("service = %+v approval=%s", server, s.configuration.MCP.ConnectionDecision(browserServiceKey))
			}
			return browser.Options(server.Settings)
		}
		if _, err := s.RunSettingsAction(ctx, 0, interaction.CommandRequest{Name: "browser", Arguments: "setup"}); err == nil {
			t.Fatal("setup without a helper succeeded")
		}
		s.browserBin = bin
		// The helper path does not exist, so the connection test fails and is
		// reported instead of claiming readiness.
		if result := act("setup"); !result.Committed || !result.ReadinessKnown || result.Ready || !strings.Contains(result.Output, "not connected") {
			t.Fatalf("setup = %+v", result)
		}
		if headed, target := choices(); headed || target != (browser.Target{}) {
			t.Fatal("setup did not use a separate headless browser")
		}
		if status, _, _ := presetStatus(s.configuration, s.mcp, browserServiceKey); status != "failed" {
			t.Fatalf("status = %q", status)
		}
		// Once the helper works, repeating setup over the unchanged definition
		// replaces the cached failure with a live connection.
		s.mcp.open = func(context.Context, mcpclient.Config) (mcpOwnedConnection, error) {
			return &mcpOwnedFixture{mcpCatalogFixture: mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("open", "Open a page")}}}, nil
		}
		if result := act("setup"); !result.Applied || !result.Ready || !strings.Contains(result.Output, "available") {
			t.Fatalf("repeat setup = %+v", result)
		}
		if status, _, _ := presetStatus(s.configuration, s.mcp, browserServiceKey); status != "ready · 1 tools" {
			t.Fatalf("status after repair = %q", status)
		}
		act("headed")
		if headed, _ := choices(); !headed {
			t.Fatal("window toggle not saved")
		}
		act("connect", "9222")
		if headed, target := choices(); !headed || target != (browser.Target{Endpoint: "9222"}) {
			t.Fatal("connection target not saved")
		}
		revision, _ := s.settingsStatus()
		if _, err := s.RunSettingsAction(ctx, revision, interaction.CommandRequest{Name: "browser", Arguments: "connect", Auth: &interaction.AuthInteraction{Input: oneValue("http://bad"), Notify: func(context.Context, interaction.AuthPrompt) error { return nil }}}); err == nil {
			t.Fatal("invalid endpoint accepted")
		}
		act("separate")
		if _, target := choices(); target != (browser.Target{}) {
			t.Fatal("separate browser not restored")
		}
		if result := act("disable"); !result.Committed || s.configuration.MCP.Servers[browserServiceKey].Enabled {
			t.Fatal("disable did not take effect")
		}
	})
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || loaded.MCP.Servers[browserServiceKey].Enabled {
		t.Fatalf("persisted service = %+v, %v", loaded.MCP.Servers[browserServiceKey], err)
	}
}

func oneValue(value string) chan string {
	values := make(chan string, 1)
	values <- value
	return values
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
			name: "login",
			session: func(t *testing.T) *interactiveSession {
				s := newLoginTestSession(authTestPaths(t))
				s.application.dependencies.codexInteractiveLogin = func(context.Context, bool, codex.LoginInteraction) (config.CodexCredentials, error) {
					return config.CodexCredentials{AccessToken: "synthetic-token", RefreshToken: "synthetic-refresh", AccountID: "fixture", ExpiresAt: time.Now().Add(time.Hour)}, nil
				}
				return s
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
