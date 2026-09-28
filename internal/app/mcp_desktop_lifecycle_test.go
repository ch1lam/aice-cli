package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// Use the real native Run owner without opening a native connection. Only the
// catalog's remote inventory is substituted, after application binding.
func managedLifecycleState(t *testing.T) *desktopState {
	t.Helper()
	manager, err := desktop.NewManager(func(context.Context) (string, string, error) {
		t.Error("lifecycle publication attempted native I/O")
		return "", "", errors.New("unexpected native resolution")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return &desktopState{close: manager.Close, bind: func(ctx context.Context, options desktop.RunOptions) (managedDesktopRun, func() error, error) {
		run, err := manager.Bind(ctx, options)
		if err != nil {
			return nil, nil, err
		}
		return run, run.Close, nil
	}}
}

func managedLifecycleCatalog(t *testing.T, state *desktopState, configuration config.Config, gate *guard.Guard) (context.Context, *mcpCatalog) {
	t.Helper()
	ctx, closeRun, err := state.bindContext(t.Context(), configuration, llm.Model{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeRun() })
	catalog, err := state.managedCatalog(ctx, configuration.MCP, nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(catalog.Close)
	return ctx, catalog
}

func selectManagedLifecycleTool(t *testing.T, ctx context.Context, catalog *mcpCatalog, gate *guard.Guard) (agent.ToolReference, *guard.MCPPermit) {
	t.Helper()
	catalog.connections[managedCUAKey] = &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("click", "fixture input")}}
	response, err := catalog.Search(ctx, tool.ToolSearchRequest{Service: managedCUAKey, Limit: 1})
	if err != nil || len(response.Selected) != 1 {
		t.Fatal("managed discovery failed", err, response)
	}
	binding, scope, found, err := catalog.MCPBinding(ctx, response.Entries[0].Name)
	if err != nil || !found {
		t.Fatal("managed binding unavailable", err)
	}
	decision, permit, err := gate.CheckMCP(ctx, response.Entries[0].Name, binding, scope)
	if err != nil || decision.Decision != guard.DecisionAllow || permit == nil {
		t.Fatal("managed authorization failed", err)
	}
	return response.Selected[0], permit
}

func TestManagedCUAApplicationIdentityAndContext(t *testing.T) {
	t.Parallel()
	state := managedLifecycleState(t)
	configuration := config.Config{DesktopEnabled: true}
	gate := ownerTestGuard(t)
	ctx, first := managedLifecycleCatalog(t, state, configuration, gate)
	_, second := managedLifecycleCatalog(t, state, configuration, gate)
	firstIdentity := first.config.Servers[managedCUAKey].Fingerprint
	if firstIdentity == "" || firstIdentity != second.config.Servers[managedCUAKey].Fingerprint {
		t.Fatal("unchanged Manager/settings did not retain identity across Runs")
	}
	other := managedLifecycleState(t)
	_, third := managedLifecycleCatalog(t, other, configuration, ownerTestGuard(t))
	if third.config.Servers[managedCUAKey].Fingerprint == firstIdentity {
		t.Fatal("different Managers shared authority")
	}
	for name, input := range map[string]context.Context{"unbound": t.Context(), "other owner": ctx} {
		t.Run(name, func(t *testing.T) {
			if _, err := other.managedCatalog(input, configuration.MCP, nil, gate); err == nil {
				t.Fatal("accepted a context without this owner's active binding")
			}
		})
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := state.managedCatalog(cancelled, configuration.MCP, nil, gate); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled Run reconstructed managed authority", err)
	}
	disabled, closeDisabled, err := state.bindContext(ctx, config.Config{}, llm.Model{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeDisabled()
	if _, err := state.managedCatalog(disabled, configuration.MCP, nil, gate); err == nil {
		t.Fatal("disabled configuration acquired managed authority")
	}
	state.invalidateManagedCatalog(gate)
	if _, err := state.managedCatalog(ctx, configuration.MCP, nil, gate); err == nil {
		t.Fatal("old live context restored removed managed policy")
	}
	_, fourth := managedLifecycleCatalog(t, state, configuration, gate)
	if fourth.config.Servers[managedCUAKey].Fingerprint == firstIdentity {
		t.Fatal("policy replacement reused an old identity")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.managedCatalog(ctx, configuration.MCP, nil, gate); err == nil {
		t.Fatal("closed Manager reconstructed authority")
	}
	if _, closeRun, err := state.bindContext(t.Context(), configuration, llm.Model{}); err == nil {
		_ = closeRun()
		t.Fatal("closed Manager created a new binding")
	}
}

func TestManagedCUASettingsPublicationInvalidatesOldAuthority(t *testing.T) {
	for _, change := range []interaction.SettingChange{
		{ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool, Bool: false}},
		{ID: "desktop_control_mode", Value: interaction.SettingValue{Kind: interaction.SettingEnum, Text: "foreground_allowed"}},
	} {
		t.Run(change.ID, func(t *testing.T) {
			s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), map[string]any{"desktop_enabled": true})
			s.desktop = managedLifecycleState(t)
			ctx, old := managedLifecycleCatalog(t, s.desktop, s.configuration, s.guard)
			ref, permit := selectManagedLifecycleTool(t, ctx, old, s.guard)
			originalIdentity := old.config.Servers[managedCUAKey].Fingerprint
			result, err := s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{change}})
			if err != nil || !result.Committed || !result.Applied {
				t.Fatal("settings publication failed", result, err)
			}
			if permit.Validate(ctx) == nil || old.Check(ctx, ref) == nil {
				t.Fatal("settings retained old authority")
			}
			if _, err := s.desktop.managedCatalog(ctx, s.configuration.MCP, nil, s.guard); err == nil {
				t.Fatal("old live context rebound after settings changed")
			}
			// Restore the original values. Neither an old catalog nor an old
			// context may revive when the same mode is enabled again.
			if change.ID == "desktop_enabled" {
				change.Value.Bool = true
			} else {
				change.Value.Text = "background_only"
			}
			result, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Revision: result.Revision, Changes: []interaction.SettingChange{change}})
			if err != nil || !result.Applied {
				t.Fatal("settings restore failed", err)
			}
			nextCtx, next := managedLifecycleCatalog(t, s.desktop, s.configuration, s.guard)
			selectManagedLifecycleTool(t, nextCtx, next, s.guard)
			if next.config.Servers[managedCUAKey].Fingerprint == originalIdentity || permit.Validate(ctx) == nil || old.Check(ctx, ref) == nil {
				t.Fatal("restoring settings revived old authority")
			}
			response, err := old.Search(ctx, tool.ToolSearchRequest{Service: managedCUAKey, Limit: 1})
			if err != nil || len(response.Selected) != 0 {
				t.Fatal("old discovery restored managed authority", err)
			}
		})
	}
}

func TestManagedCUAUnpublishedOrUnrelatedSettingsPreserveAuthority(t *testing.T) {
	s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), map[string]any{"desktop_enabled": true})
	s.desktop = managedLifecycleState(t)
	ctx, old := managedLifecycleCatalog(t, s.desktop, s.configuration, s.guard)
	ref, permit := selectManagedLifecycleTool(t, ctx, old, s.guard)
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"broken":`)
	result, err := s.ApplySettings(t.Context(), interaction.SettingsRequest{Changes: []interaction.SettingChange{{ID: "desktop_enabled", Value: interaction.SettingValue{Kind: interaction.SettingBool}}}})
	if err == nil || result.Committed || !s.configuration.DesktopEnabled || permit.Validate(ctx) != nil || old.Check(ctx, ref) != nil {
		t.Fatal("failed save changed managed authority", err)
	}
	writeConfigFixture(t, s.configuration.Paths.GlobalSettings, `{"desktop_enabled":true}`)
	result, err = s.ApplySettings(t.Context(), interaction.SettingsRequest{Revision: result.Revision, Changes: []interaction.SettingChange{{ID: "max_turns", Value: interaction.SettingValue{Kind: interaction.SettingInt, Int: 100}}}})
	if err != nil || !result.Applied || permit.Validate(ctx) != nil || old.Check(ctx, ref) != nil {
		t.Fatal("unrelated settings changed managed authority", err)
	}
	_, next := managedLifecycleCatalog(t, s.desktop, s.configuration, s.guard)
	if next.config.Servers[managedCUAKey].Fingerprint != old.config.Servers[managedCUAKey].Fingerprint {
		t.Fatal("unrelated settings replaced managed identity")
	}
}

func TestManagedCUAMCPReplacementInvalidatesEvenOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "preparation failure"}[fail], func(t *testing.T) {
			s := webCommandSession(t, testWebBackends(&fakeWireBackend{}, &fakeFetch{}), map[string]any{"desktop_enabled": true})
			s.desktop = managedLifecycleState(t)
			ctx, old := managedLifecycleCatalog(t, s.desktop, s.configuration, s.guard)
			ref, permit := selectManagedLifecycleTool(t, ctx, old, s.guard)
			if fail {
				s.application.dependencies.newModel = func(config.Config) (llm.Streamer, error) { return nil, errors.New("fixture model preparation failed") }
			}
			next := s.configuration
			next.MCP.Restrictions = []config.MCPRestriction{{Source: "*", Server: "cua", Tools: []string{"click"}}}
			if err := s.beginSettingsOperation(nil, true); err != nil {
				t.Fatal(err)
			}
			err := s.publishMCPConfiguration(next)
			s.endSettingsOperation(true)
			if (err != nil) != fail || permit.Validate(ctx) == nil || old.Check(ctx, ref) == nil {
				t.Fatal("MCP replacement retained managed authority", err)
			}
			if fail {
				if s.loop != nil || s.modelErr == nil || !strings.Contains(s.modelErr.Error(), "runtime update failed") {
					t.Fatal("failed replacement left executable runtime")
				}
				return
			}
			t.Cleanup(func() { _ = s.mcp.Close() })
			nextCtx, catalog := managedLifecycleCatalog(t, s.desktop, s.configuration, s.guard)
			catalog.connections[managedCUAKey] = &mcpCatalogFixture{items: []mcpclient.Tool{catalogFixtureTool("click", "fixture input")}}
			response, err := catalog.Search(nextCtx, tool.ToolSearchRequest{Service: managedCUAKey, Limit: 1})
			if err != nil || len(response.Selected) != 0 {
				t.Fatal("new managed catalog ignored narrowed restrictions", err)
			}
		})
	}
}
