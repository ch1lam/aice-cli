package config

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// MCPPatch edits user-owned entries only. A nil server removes that entry;
// Enabled changes only a saved toggle, preserving concurrently edited fields.
type MCPPatch struct {
	// CreateOnly prevents concurrent additions from replacing an existing ID.
	CreateOnly   bool
	Servers      map[string]*MCPServerSettings
	Enabled      map[string]bool
	Restrictions *[]MCPRestriction
}

func (p MCPPatch) Apply(settings MCPSettings) (MCPSettings, error) {
	next := MCPSettings{Servers: make(map[string]MCPServerSettings, len(settings.Servers)), Restrictions: cloneMCPRestrictions(settings.Restrictions)}
	for id, server := range settings.Servers {
		next.Servers[id] = cloneMCPServer(server)
	}
	for id, server := range p.Servers {
		if _, exists := settings.Servers[id]; p.CreateOnly && exists {
			return MCPSettings{}, fmt.Errorf("config: MCP server already exists")
		}
		if !mcpID(id) || id == ManagedCUAServerID {
			return MCPSettings{}, fmt.Errorf("config: invalid or reserved MCP server ID")
		}
		if _, duplicate := p.Enabled[id]; duplicate {
			return MCPSettings{}, fmt.Errorf("config: MCP entry replacement and toggle overlap")
		}
		if server == nil {
			delete(next.Servers, id)
		} else {
			next.Servers[id] = cloneMCPServer(*server)
		}
	}
	for id, enabled := range p.Enabled {
		server, ok := next.Servers[id]
		if !ok {
			return MCPSettings{}, fmt.Errorf("config: MCP toggle requires an existing user server")
		}
		server.Enabled = &enabled
		next.Servers[id] = server
	}
	if p.Restrictions != nil {
		next.Restrictions = cloneMCPRestrictions(*p.Restrictions)
	}
	return next, next.Validate()
}

// SaveMCPPatch uses the shared atomic writer and returns actual commit status.
// It never persists the merged project layer, resolved credentials, or grants.
func SaveMCPPatch(ctx context.Context, paths Paths, patch MCPPatch) (MCPSettings, CommitResult, error) {
	if err := paths.validate(); err != nil {
		return MCPSettings{}, CommitResult{}, err
	}
	var saved MCPSettings
	commit, err := editFile(ctx, paths.GlobalSettings, func(values map[string]any) error {
		var raw json.RawMessage
		if existing, ok := values[mcpSettingsKey]; ok {
			raw, _ = json.Marshal(existing)
		}
		settings, err := decodeMCPSettings(raw)
		if err != nil {
			return fmt.Errorf("existing MCP settings left unchanged: %w", err)
		}
		next, err := patch.Apply(settings)
		if err != nil {
			return err
		}
		values[mcpSettingsKey] = next
		saved = next
		return nil
	})
	return saved, commit, err
}

// SaveMCPCredential stores one explicit slot for the selected configuration
// identity. Empty value removes it. It cannot write a provider credential or
// transfer a slot to a changed source, endpoint, command or environment binding.
func SaveMCPCredential(ctx context.Context, paths Paths, server MCPServer, slot, value string) (CommitResult, error) {
	if err := paths.validate(); err != nil {
		return CommitResult{}, err
	}
	if !mcpID(slot) || !mcpText(value, 8192) {
		return CommitResult{}, fmt.Errorf("config: invalid MCP credential slot (value omitted)")
	}
	if !mcpID(server.ID) || server.ID == ManagedCUAServerID || server.Settings.validate() != nil {
		return CommitResult{}, fmt.Errorf("config: invalid MCP credential binding")
	}
	expectedPath := paths.GlobalSettings
	switch server.Source.Kind {
	case "user":
	case "project":
		expectedPath = paths.ProjectSettings
	default:
		return CommitResult{}, fmt.Errorf("config: invalid MCP credential source")
	}
	if expectedPath == "" {
		return CommitResult{}, fmt.Errorf("config: MCP credential source is unavailable")
	}
	absolute, err := filepath.Abs(expectedPath)
	if err != nil || filepath.Clean(absolute) != server.Source.Location {
		return CommitResult{}, fmt.Errorf("config: MCP credential source changed")
	}
	expected, err := resolveMCPServer(server.ID, server.Source, server.Settings, nil, nil)
	if err != nil || expected.Key != server.Key || expected.CredentialScope != server.CredentialScope {
		return CommitResult{}, fmt.Errorf("config: MCP credential binding changed")
	}
	return editFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		credentials := make(mcpCredentials)
		if raw, ok := values[mcpCredentialsKey]; ok {
			decoded, err := decodeMCPCredentials(raw)
			if err != nil {
				return fmt.Errorf("existing MCP credentials left unchanged: %w", err)
			}
			credentials = decoded
		}
		setMCPCredential(credentials, server.Key, server.CredentialScope, slot, value)
		if _, err := decodeMCPCredentials(credentials); err != nil {
			return err
		}
		values[mcpCredentialsKey] = credentials
		return nil
	})
}
