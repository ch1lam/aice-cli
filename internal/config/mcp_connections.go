package config

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
)

const mcpConnectionsKey = "mcp_connections"

// MCPConnectionDecision authorizes establishing a connection, never executing
// tools. An absent or changed binding needs approval even for trusted projects.
type MCPConnectionDecision string

const (
	MCPConnectionAsk   MCPConnectionDecision = "ask"
	MCPConnectionAllow MCPConnectionDecision = "allow"
	MCPConnectionDeny  MCPConnectionDecision = "deny"
)

type mcpConnectionApproval struct {
	Fingerprint string                `json:"fingerprint"`
	Decision    MCPConnectionDecision `json:"decision"`
}

func (m MCPConfig) ConnectionDecision(key string) MCPConnectionDecision {
	server, ok := m.Servers[key]
	approval := m.connections[key]
	if !ok || server.Fingerprint == "" || approval.Fingerprint != server.Fingerprint {
		return MCPConnectionAsk
	}
	if approval.Decision == MCPConnectionAllow || approval.Decision == MCPConnectionDeny {
		return approval.Decision
	}
	return MCPConnectionAsk
}

func decodeMCPConnections(raw any) (map[string]mcpConnectionApproval, error) {
	data, err := json.Marshal(raw)
	var approvals map[string]mcpConnectionApproval
	if err != nil || len(data) > maxMCPSettingsBytes || jsonutil.DecodeStrict(data, &approvals) != nil || approvals == nil || len(approvals) > 1024 {
		return nil, fmt.Errorf("config: invalid MCP connection approvals (values omitted)")
	}
	for key, approval := range approvals {
		digest, err := hex.DecodeString(approval.Fingerprint)
		if key == "" || !mcpText(key, 256) || err != nil || len(digest) != 32 || approval.Decision != MCPConnectionAllow && approval.Decision != MCPConnectionDeny {
			return nil, fmt.Errorf("config: invalid MCP connection approval binding")
		}
	}
	return approvals, nil
}

// WithMCPConnectionDecision publishes an explicitly made decision to a frozen
// configuration after saving. Ask removes the decision. It performs no I/O.
func (c Config) WithMCPConnectionDecision(key, fingerprint string, decision MCPConnectionDecision) (Config, error) {
	server, ok := c.MCP.Servers[key]
	if !ok || server.Fingerprint != fingerprint || fingerprint == "" || !validMCPConnectionDecision(decision) {
		return Config{}, fmt.Errorf("config: MCP connection approval identity changed")
	}
	next := c
	next.mcpInputs.connections = maps.Clone(c.mcpInputs.connections)
	if next.mcpInputs.connections == nil {
		next.mcpInputs.connections = make(map[string]mcpConnectionApproval)
	}
	setMCPConnectionDecision(next.mcpInputs.connections, key, fingerprint, decision)
	if _, err := decodeMCPConnections(next.mcpInputs.connections); err != nil {
		return Config{}, err
	}
	next.MCP = c.MCP.Clone()
	next.MCP.connections = maps.Clone(next.mcpInputs.connections)
	return next, nil
}

// SaveMCPConnectionDecision writes only a user-owned, exact-identity decision.
// Project settings cannot grant connection access, and --yolo never saves one.
func SaveMCPConnectionDecision(ctx context.Context, paths Paths, server MCPServer, decision MCPConnectionDecision) (CommitResult, error) {
	if !validMCPConnectionDecision(decision) {
		return CommitResult{}, fmt.Errorf("config: invalid MCP connection decision")
	}
	if err := validateMCPStoredBinding(paths, server); err != nil {
		return CommitResult{}, err
	}
	return editFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		approvals := make(map[string]mcpConnectionApproval)
		if raw, ok := values[mcpConnectionsKey]; ok {
			decoded, err := decodeMCPConnections(raw)
			if err != nil {
				return err
			}
			approvals = decoded
		}
		setMCPConnectionDecision(approvals, server.Key, server.Fingerprint, decision)
		if _, err := decodeMCPConnections(approvals); err != nil {
			return err
		}
		values[mcpConnectionsKey] = approvals
		return nil
	})
}

func validMCPConnectionDecision(d MCPConnectionDecision) bool {
	return d == MCPConnectionAsk || d == MCPConnectionAllow || d == MCPConnectionDeny
}
func setMCPConnectionDecision(approvals map[string]mcpConnectionApproval, key, fingerprint string, decision MCPConnectionDecision) {
	if decision == MCPConnectionAsk {
		delete(approvals, key)
	} else {
		approvals[key] = mcpConnectionApproval{fingerprint, decision}
	}
}

func validateMCPStoredBinding(paths Paths, server MCPServer) error {
	if err := paths.validate(); err != nil {
		return err
	}
	if !mcpID(server.ID) || server.ID == ManagedCUAServerID || server.Settings.validate() != nil {
		return fmt.Errorf("config: invalid MCP connection decision")
	}
	source := paths.GlobalSettings
	if server.Source.Kind == "project" {
		source = paths.ProjectSettings
	} else if server.Source.Kind != "user" {
		source = ""
	}
	absolute, err := filepath.Abs(source)
	expected, resolveErr := resolveMCPServer(server.ID, server.Source, server.Settings, nil, nil)
	fingerprint := server.connectionFingerprint()
	if source == "" || err != nil || filepath.Clean(absolute) != server.Source.Location || resolveErr != nil || expected.Key != server.Key || expected.CredentialScope != server.CredentialScope || fingerprint != server.Fingerprint {
		return fmt.Errorf("config: MCP connection approval identity changed")
	}
	return nil
}

// ForgetMCPServiceAccess removes every credential scope and connection decision
// for one source-qualified service. Removal never touches provider credentials.
func ForgetMCPServiceAccess(ctx context.Context, paths Paths, server MCPServer) (CommitResult, error) {
	if err := validateMCPStoredBinding(paths, server); err != nil {
		return CommitResult{}, err
	}
	return editFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		credentials := make(mcpCredentials)
		if raw, ok := values[mcpCredentialsKey]; ok {
			decoded, err := decodeMCPCredentials(raw)
			if err != nil {
				return err
			}
			credentials = decoded
		}
		approvals := make(map[string]mcpConnectionApproval)
		if raw, ok := values[mcpConnectionsKey]; ok {
			decoded, err := decodeMCPConnections(raw)
			if err != nil {
				return err
			}
			approvals = decoded
		}
		if err := clearMCPPermissions(values, server.Key); err != nil {
			return err
		}
		delete(credentials, server.Key)
		delete(approvals, server.Key)
		if raw, ok := values[mcpOAuthKey]; ok {
			oauth, err := decodeMCPOAuth(raw)
			if err != nil {
				return err
			}
			delete(oauth, server.Key)
			values[mcpOAuthKey] = oauth
		}
		values[mcpCredentialsKey], values[mcpConnectionsKey] = credentials, approvals
		return nil
	})
}

func (c Config) WithoutMCPServiceAccess(key string) (Config, error) {
	if _, ok := c.MCP.Servers[key]; !ok {
		return Config{}, fmt.Errorf("config: MCP service is unavailable")
	}
	next := c
	next.mcpInputs.credentials = cloneMCPCredentials(c.mcpInputs.credentials)
	next.mcpInputs.connections = maps.Clone(c.mcpInputs.connections)
	next.mcpInputs.oauth = cloneMCPOAuth(c.mcpInputs.oauth)
	next.mcpInputs.permissions = cloneMCPPermissions(c.mcpInputs.permissions)
	delete(next.mcpInputs.permissions, key)
	delete(next.mcpInputs.credentials, key)
	delete(next.mcpInputs.connections, key)
	delete(next.mcpInputs.oauth, key)
	effective, err := effectiveMCP(next.mcpInputs, c.Paths, c.environmentLookup)
	if err != nil {
		return Config{}, err
	}
	next.MCP = effective
	return next, nil
}
