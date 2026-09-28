package config

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const mcpPermissionsKey = "mcp_permissions"

// MCPPermission is an explicit user rule, never a saved Session approval.
// Allow requires the reviewed schema; deny survives schema changes within the
// same service connection and configured permission scope. Ask removes a rule.
type MCPPermission struct {
	Operation         string `json:"operation,omitempty"`
	Tool              string `json:"tool"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	Decision          string `json:"decision"`
}

// MCPPermissionRules is a copied set for matching a whole catalog without
// repeatedly hashing the configured scope for each tool.
type MCPPermissionRules []MCPPermission

func (rules MCPPermissionRules) Decision(operation, name, schema string) string {
	for _, rule := range rules {
		if rule.Operation == operation && rule.Tool == name && (rule.Decision == "deny" || rule.SchemaFingerprint == schema) {
			return rule.Decision
		}
	}
	return "ask"
}

type mcpPermissionSet struct {
	Fingerprint string          `json:"fingerprint"`
	Scope       string          `json:"scope"`
	Rules       []MCPPermission `json:"rules"`
}
type mcpPermissions map[string]mcpPermissionSet

// PermissionScope binds rules to the configured upper bound, independently of
// those rules. Runtime policy also includes PermissionRevision to reject stale
// catalogs after a user edits a rule.
func (m MCPConfig) PermissionScope(key string) string {
	s := m.Servers[key].Settings
	return "ordinary-mcp-tools:" + mcpDigest(struct {
		Include      *[]string
		Exclude      []string
		Restrictions []MCPRestriction
	}{s.IncludeTools, s.ExcludeTools, m.Restrictions})
}

func (m MCPConfig) Permissions(key string) MCPPermissionRules {
	return slices.Clone(m.permissionRules(key))
}

// StoredPermissions exposes inactive records for explicit review/removal. They
// are not authority; only Permissions and PermissionDecision match the binding.
func (m MCPConfig) StoredPermissions(key string) (fingerprint, scope string, rules []MCPPermission) {
	set := m.permissions[key]
	return set.Fingerprint, set.Scope, slices.Clone(set.Rules)
}

func (m MCPConfig) permissionRules(key string) []MCPPermission {
	s, ok := m.Servers[key]
	set := m.permissions[key]
	if !ok || set.Fingerprint != s.Fingerprint || set.Scope != m.PermissionScope(key) {
		return nil
	}
	return set.Rules
}
func (m MCPConfig) PermissionRevision(key string) string { return mcpDigest(m.permissionRules(key)) }
func (m MCPConfig) PermissionDecision(key, operation, name, schema string) string {
	return MCPPermissionRules(m.permissionRules(key)).Decision(operation, name, schema)
}

func (r MCPPermission) valid() bool {
	if r.Operation != "" && r.Operation != llm.OperationResourceRead || r.Operation == llm.OperationResourceRead && r.Tool != llm.OperationResourceRead ||
		strings.TrimSpace(r.Tool) == "" || !mcpText(r.Tool, 4096) || !mcpHexDigest(r.SchemaFingerprint) || r.Decision != "allow" && r.Decision != "deny" && r.Decision != "ask" {
		return false
	}
	return !strings.ContainsFunc(r.Tool, unicode.IsControl)
}
func decodeMCPPermissions(raw any) (mcpPermissions, error) {
	data, err := json.Marshal(raw)
	var sets mcpPermissions
	if err != nil || len(data) > maxMCPSettingsBytes || jsonutil.DecodeStrict(data, &sets) != nil || sets == nil || len(sets) > 1024 {
		return nil, fmt.Errorf("config: invalid MCP user permission rules (values omitted)")
	}
	for key, set := range sets {
		if key == "" || !mcpText(key, 256) || !mcpHexDigest(set.Fingerprint) || !strings.HasPrefix(set.Scope, "ordinary-mcp-tools:") || !mcpHexDigest(strings.TrimPrefix(set.Scope, "ordinary-mcp-tools:")) || len(set.Rules) > 2001 || len(set.Rules) == 0 {
			return nil, fmt.Errorf("config: invalid MCP permission binding")
		}
		seen := make(map[[2]string]bool)
		for _, rule := range set.Rules {
			key := [2]string{rule.Operation, rule.Tool}
			if !rule.valid() || rule.Decision == "ask" || seen[key] {
				return nil, fmt.Errorf("config: invalid or duplicate MCP permission rule")
			}
			seen[key] = true
		}
	}
	return sets, nil
}
func cloneMCPPermissions(sets mcpPermissions) mcpPermissions {
	next := make(mcpPermissions, len(sets))
	for key, set := range sets {
		set.Rules = slices.Clone(set.Rules)
		next[key] = set
	}
	return next
}
func readMCPPermissions(values map[string]any) (mcpPermissions, error) {
	if raw, ok := values[mcpPermissionsKey]; ok {
		return decodeMCPPermissions(raw)
	}
	return make(mcpPermissions), nil
}
func clearMCPPermissions(values map[string]any, key string) error {
	sets, err := readMCPPermissions(values)
	if err != nil {
		return err
	}
	delete(sets, key)
	if _, exists := values[mcpPermissionsKey]; exists {
		values[mcpPermissionsKey] = sets
	}
	return nil
}
func setMCPPermission(sets mcpPermissions, server MCPServer, scope string, rule MCPPermission) {
	set := sets[server.Key]
	if rule.Decision != "ask" && (set.Fingerprint != server.Fingerprint || set.Scope != scope) {
		set = mcpPermissionSet{Fingerprint: server.Fingerprint, Scope: scope}
	}
	set.Rules = slices.DeleteFunc(set.Rules, func(old MCPPermission) bool { return old.Operation == rule.Operation && old.Tool == rule.Tool })
	if rule.Decision != "ask" {
		set.Rules = append(set.Rules, rule)
	}
	slices.SortFunc(set.Rules, func(a, b MCPPermission) int {
		if cmp := strings.Compare(a.Operation, b.Operation); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Tool, b.Tool)
	})
	if len(set.Rules) == 0 {
		delete(sets, server.Key)
	} else {
		sets[server.Key] = set
	}
}

func (c Config) WithMCPPermission(key, fingerprint string, rule MCPPermission) (Config, error) {
	server, ok := c.MCP.Servers[key]
	if !ok || fingerprint == "" || server.Fingerprint != fingerprint || !rule.valid() {
		return Config{}, fmt.Errorf("config: invalid MCP permission identity or rule")
	}
	next := c
	next.mcpInputs.permissions = cloneMCPPermissions(c.mcpInputs.permissions)
	setMCPPermission(next.mcpInputs.permissions, server, c.MCP.PermissionScope(key), rule)
	if _, err := decodeMCPPermissions(next.mcpInputs.permissions); err != nil {
		return Config{}, err
	}
	next.MCP = c.MCP.Clone()
	next.MCP.permissions = cloneMCPPermissions(next.mcpInputs.permissions)
	return next, nil
}

// SaveMCPPermission changes one rule under the existing user auth-file lock.
// The caller supplies the reviewed schema and frozen configuration; no discovery,
// credential resolution or remote execution occurs in the writer.
func SaveMCPPermission(ctx context.Context, paths Paths, configuration MCPConfig, key string, rule MCPPermission) (CommitResult, error) {
	server, ok := configuration.Servers[key]
	if !ok || !rule.valid() {
		return CommitResult{}, fmt.Errorf("config: invalid MCP permission rule")
	}
	if err := validateMCPStoredBinding(paths, server); err != nil {
		return CommitResult{}, err
	}
	return editFile(ctx, paths.GlobalAuth, func(values map[string]any) error {
		sets, err := readMCPPermissions(values)
		if err != nil {
			return err
		}
		setMCPPermission(sets, server, configuration.PermissionScope(key), rule)
		if _, err := decodeMCPPermissions(sets); err != nil {
			return err
		}
		values[mcpPermissionsKey] = sets
		return nil
	})
}
