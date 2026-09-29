package app

import (
	"context"
	"fmt"
	"maps"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/guard"
)

const (
	managedCUAKey           = "managed:cua"
	managedCUAPolicyVersion = "1"
)

// This private binding is supplied by the application, never parsed from MCP
// settings. Production supplies the native admitted Run through an
// application-owned context; tests may inject its consumer interface.
type managedCUACatalogBinding struct {
	connection    mcpCatalogConnection
	ownerIdentity string
	mode          desktop.ControlMode
}

// The app owns the binding; production supplies desktop.Run. This consumer
// boundary also permits lifecycle observers and offline tests without giving
// ordinary MCP configuration a way to construct managed authority.
type managedDesktopRun interface {
	mcpCatalogConnection
	ControlMode() desktop.ControlMode
}

// managedCatalog accepts only this owner's live, enabled Run context. Callers
// cannot choose the managed identity or reconstruct authority from a Session.
// The catalog borrows the Run; close the catalog before closing that Run.
func (d *desktopState) managedCatalog(ctx context.Context, configuration config.MCPConfig, connections map[string]mcpCatalogConnection, gate *guard.Guard) (*mcpCatalog, error) {
	run, err := d.bound(ctx)
	if err != nil {
		return nil, err
	}
	binding := ctx.Value(desktopContextKey{}).(desktopRunBinding)
	d.managedMu.Lock()
	defer d.managedMu.Unlock()
	if binding.managedIdentity == "" || binding.managedIdentity != d.managedIdentity {
		return nil, fmt.Errorf("managed CUA settings changed; start a new Run")
	}
	return buildMCPCatalog(configuration, connections, gate, &managedCUACatalogBinding{connection: run, ownerIdentity: binding.managedIdentity, mode: run.ControlMode()})
}

// invalidateManagedCatalog runs under the application's idle settings
// reservation, after saving the new policy. Rotating the identity prevents an
// old Run or catalog from restoring authority even if settings change back.
// It neither connects nor closes the shared native service.
func (d *desktopState) invalidateManagedCatalog(gate *guard.Guard) {
	if d != nil {
		d.managedMu.Lock()
		defer d.managedMu.Unlock()
		d.managedIdentity = ""
	}
	gate.RemoveMCPService("managed:computer-use", config.ManagedCUAServerID)
}

// The permission inventory is deliberately separate from Driver discovery.
// Adding a native capability does not automatically extend Computer Use grants.
func managedCUAToolNames() []string {
	return []string{"list_apps", "list_windows", "get_window_state", "launch_app", "click", "drag", "type_text", "set_value", "press_key", "hotkey", "scroll"}
}

// managedCUAToolGrants translates a validated application binding into the
// catalog's frozen permission input. Ordinary MCP configuration cannot create it,
// and newly advertised Driver tools do not extend the reviewed inventory.
func managedCUAToolGrants(managed *managedCUACatalogBinding) map[string][]string {
	if managed == nil {
		return nil
	}
	return map[string][]string{managedCUAKey: managedCUAToolNames()}
}

func withManagedCUACatalog(configuration config.MCPConfig, connections map[string]mcpCatalogConnection, managed *managedCUACatalogBinding) (config.MCPConfig, map[string]mcpCatalogConnection, error) {
	for key, server := range configuration.Servers {
		if key == managedCUAKey || server.ID == config.ManagedCUAServerID || server.Source.Kind == "managed" {
			return config.MCPConfig{}, nil, fmt.Errorf("managed CUA identity cannot be supplied by ordinary MCP configuration")
		}
	}
	if managed == nil {
		return configuration, connections, nil
	}
	if managed.connection == nil || managed.ownerIdentity == "" || len(managed.ownerIdentity) > 256 ||
		(managed.mode != desktop.BackgroundOnly && managed.mode != desktop.ForegroundAllowed) || len(configuration.Servers) >= 129 {
		return config.MCPConfig{}, nil, fmt.Errorf("invalid managed CUA application binding")
	}
	if _, exists := connections[managedCUAKey]; exists {
		return config.MCPConfig{}, nil, fmt.Errorf("managed CUA connection cannot be supplied twice")
	}
	next := configuration.Clone()
	names := managedCUAToolNames()
	next.Servers[managedCUAKey] = config.MCPServer{
		Key: managedCUAKey, ID: config.ManagedCUAServerID,
		Source: config.Source{Kind: "managed", Location: "computer-use"}, Enabled: true,
		Fingerprint: mcpDigest([]string{"managed-cua", desktop.DriverVersion, desktop.ProtocolVersion, managed.ownerIdentity}),
		Settings:    config.MCPServerSettings{Name: "Computer Use", IncludeTools: &names},
	}
	borrowed := maps.Clone(connections)
	if borrowed == nil {
		borrowed = make(map[string]mcpCatalogConnection)
	}
	borrowed[managedCUAKey] = managed.connection
	return next, borrowed, nil
}
