package guard

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// MCPService is a policy snapshot published by the application after binding
// configuration and a catalog. Neither model arguments nor remote annotations
// are policy inputs. Publication cannot create user rules or connection approval; UserDecision
// comes only from an explicit, identity-matched user rule.
type MCPService struct {
	Source                string
	ServiceID             string
	ConnectionFingerprint string
	PermissionScope       string
	Enabled               bool
	Tools                 []MCPToolPolicy
}

// MCPToolPolicy describes one catalog version and the configured upper bound.
// An absent or disallowed tool cannot be enabled by a Session grant or yolo.
type MCPToolPolicy struct {
	Operation         string
	Name              string
	SchemaFingerprint string
	Allowed           bool
	// UserDecision is empty/ask unless app matched an explicit user rule.
	UserDecision Decision
}

type mcpServiceKey struct{ source, id string }

type mcpToolState struct {
	MCPToolPolicy
	epoch uint64
}

type mcpServiceState struct {
	connection, scope string
	enabled, revoked  bool
	epoch             uint64
	tools             map[string]mcpToolState
	granted           map[string]uint64
}

// MCPPermit retains an immutable check snapshot, never a remote handle. Its
// methods recheck the current bound policy before reusing or granting authority.
// It is intentionally transient and must not be reconstructed from history.
type MCPPermit struct {
	guard                *Guard
	key                  mcpServiceKey
	binding              llm.ToolBinding
	scope                string
	session, epoch, tool uint64
	serviceTools         map[string]uint64
}

// SetMCPService replaces a service's current policy. Unchanged tools retain
// grants; connection/scope/enable changes clear them all, while individual
// schema/filter changes clear just that tool. Reverting a change never revives
// an earlier grant. An explicit revocation survives subsequent publications.
func (g *Guard) SetMCPService(input MCPService) error {
	return g.setMCPService(input, "replace")
}

// BindMCPService initializes a policy only if absent. A run with an older
// configuration must not overwrite the current application-owned identity.
func (g *Guard) BindMCPService(input MCPService) error {
	return g.setMCPService(input, "bind")
}

// RefreshMCPTools publishes discovery only while the expected configuration is
// still current. It cannot undo a disable, removal, revocation or config edit.
func (g *Guard) RefreshMCPTools(input MCPService) error {
	return g.setMCPService(input, "refresh")
}

// RefreshMCPResources replaces the resource-read policy without changing the
// independently discovered ordinary tool catalog.
func (g *Guard) RefreshMCPResources(input MCPService) error {
	return g.setMCPService(input, "refresh-resources")
}

// MCPServiceAvailable checks the current upper bound before discovery. It is
// not connection authorization and does not grant any tool execution authority.
func (g *Guard) MCPServiceAvailable(source, id, connection, scope string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	state := g.mcpServices[mcpServiceKey{source, id}]
	return state != nil && state.enabled && !state.revoked && state.connection == connection && state.scope == scope
}

func (g *Guard) setMCPService(input MCPService, operation string) error {
	if g == nil {
		return fmt.Errorf("MCP execution gate is unavailable")
	}
	for _, value := range []string{input.Source, input.ServiceID, input.ConnectionFingerprint, input.PermissionScope} {
		if !validMCPIdentity(value) {
			return fmt.Errorf("MCP policy identity is invalid")
		}
	}
	if len(input.Tools) > 2001 {
		return fmt.Errorf("MCP policy catalog exceeds the tool limit")
	}
	next := make(map[string]mcpToolState, len(input.Tools))
	ordinary, resources := 0, 0
	refreshing := operation == "refresh" || operation == "refresh-resources"
	for _, tool := range input.Tools {
		if tool.Operation != "" && tool.Operation != llm.OperationResourceRead {
			return fmt.Errorf("MCP operation is invalid")
		}
		if tool.Operation == "" {
			ordinary++
		} else {
			resources++
		}
		if ordinary > 2000 || resources > 1 {
			return fmt.Errorf("MCP operation catalog exceeds its bound")
		}
		if refreshing && ((operation == "refresh") != (tool.Operation == "")) {
			return fmt.Errorf("MCP refresh contains a different operation kind")
		}
		if tool.UserDecision != "" && tool.UserDecision != DecisionAsk && tool.UserDecision != DecisionAllow && tool.UserDecision != DecisionDeny {
			return fmt.Errorf("MCP user permission decision is invalid")
		}
		key := mcpOperationKey(tool.Operation, tool.Name)
		if !validMCPIdentity(tool.Name) || !validMCPIdentity(tool.SchemaFingerprint) {
			return fmt.Errorf("MCP tool policy identity is invalid")
		}
		if _, exists := next[key]; exists {
			return fmt.Errorf("MCP policy catalog contains duplicate tools")
		}
		next[key] = mcpToolState{MCPToolPolicy: tool}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := mcpServiceKey{input.Source, input.ServiceID}
	previous := g.mcpServices[key]
	if operation != "replace" && previous != nil {
		if previous.connection != input.ConnectionFingerprint || previous.scope != input.PermissionScope ||
			previous.enabled != input.Enabled || previous.revoked {
			return fmt.Errorf("MCP policy changed before catalog publication")
		}
		if operation == "bind" {
			return nil
		}
	}
	if refreshing && previous == nil {
		return fmt.Errorf("MCP binding was removed before catalog publication")
	}
	if refreshing {
		// Preserve the other operation domain under the same lock. A tool refresh
		// must not remove resource grants, or let a same-name tool inherit them.
		for name, tool := range previous.tools {
			if (operation == "refresh") != (tool.Operation == "") {
				next[name] = tool
			}
		}
		for name, tool := range next {
			if old, exists := previous.tools[name]; exists && !old.Allowed && tool.Allowed {
				return fmt.Errorf("MCP catalog refresh cannot lift a configured tool denial")
			}
		}
	}
	// Two 64-entry configuration collections plus the managed CUA instance.
	if previous == nil && len(g.mcpServices) >= 129 {
		return fmt.Errorf("MCP execution gate exceeds the service limit")
	}
	if g.mcpServices == nil {
		g.mcpServices = make(map[mcpServiceKey]*mcpServiceState)
	}
	state := &mcpServiceState{
		connection: input.ConnectionFingerprint, scope: input.PermissionScope,
		enabled: input.Enabled, tools: next, granted: make(map[string]uint64),
	}
	if previous != nil {
		state.revoked = previous.revoked
	}
	sameService := previous != nil && previous.connection == state.connection &&
		previous.scope == state.scope && previous.enabled == state.enabled
	if sameService {
		state.epoch = previous.epoch
	} else {
		g.mcpEpoch++
		state.epoch = g.mcpEpoch
	}
	for name, tool := range next {
		if sameService && previous.tools[name].MCPToolPolicy == tool.MCPToolPolicy {
			tool.epoch = previous.tools[name].epoch
			if previous.granted[name] == tool.epoch {
				state.granted[name] = tool.epoch
			}
		} else {
			g.mcpEpoch++
			tool.epoch = g.mcpEpoch
		}
		next[name] = tool
	}
	g.mcpServices[key] = state
	return nil
}

// RevokeMCPService immediately denies further calls and invalidates pending
// approvals. RestoreMCPService is a separate, explicit user-management action.
func (g *Guard) RevokeMCPService(source, id string) {
	g.setMCPRevoked(source, id, true)
}

// RestoreMCPService lifts only the explicit revocation, without restoring any
// grants. Disabled services, filters and current identity checks still apply.
func (g *Guard) RestoreMCPService(source, id string) {
	g.setMCPRevoked(source, id, false)
}

func (g *Guard) setMCPRevoked(source, id string, revoked bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if state := g.mcpServices[mcpServiceKey{source, id}]; state != nil {
		g.mcpEpoch++
		state.epoch = g.mcpEpoch
		state.revoked = revoked
		clear(state.granted)
	}
}

// RemoveMCPService forgets a removed binding. Re-adding it starts without any
// authority, even if the connection fingerprint and schema are unchanged.
func (g *Guard) RemoveMCPService(source, id string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.mcpServices, mcpServiceKey{source, id})
}

// CheckMCP checks the application's frozen tool binding against current policy.
// Generic unknown-name grants and the legacy Guard enabled flag do not apply.
// Connection authorization is separate and must precede service discovery.
func (g *Guard) CheckMCP(ctx context.Context, modelName string, binding llm.ToolBinding, scope string) (Result, *MCPPermit, error) {
	if ctx == nil {
		return Result{}, nil, fmt.Errorf("guard: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, nil, err
	}
	action := Action{Kind: "mcp", ToolName: modelName, Target: binding.Source + ":" + binding.ServiceID}
	deny := Result{Decision: DecisionDeny, Reason: "MCP tool binding is unavailable or no longer permitted", RuleID: "mcp.unavailable", Action: action}
	if g == nil {
		return deny, nil, nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	key := mcpServiceKey{binding.Source, binding.ServiceID}
	state := g.mcpServices[key]
	if !matchesMCPBinding(state, binding, scope) {
		return deny, nil, nil
	}
	tool := state.tools[mcpOperationKey(binding.Operation, binding.ToolName)]
	permit := &MCPPermit{
		guard: g, key: key, binding: binding, scope: scope, session: g.mcpSession,
		epoch: state.epoch, tool: tool.epoch, serviceTools: make(map[string]uint64),
	}
	for name, candidate := range state.tools {
		if candidate.Allowed && candidate.UserDecision != DecisionDeny {
			permit.serviceTools[name] = candidate.epoch
		}
	}
	if tool.UserDecision == DecisionAllow || state.granted[mcpOperationKey(binding.Operation, binding.ToolName)] == tool.epoch {
		return Result{Decision: DecisionAllow, Action: action}, permit, nil
	}
	reason := fmt.Sprintf("MCP tool %q from %q (source %q) requires confirmation", binding.ToolName, binding.ServiceID, binding.Source)
	if binding.Operation == llm.OperationResourceRead {
		reason = fmt.Sprintf("MCP resource read from %q (source %q) requires confirmation; Session approval covers any URI on this service", binding.ServiceID, binding.Source)
	}
	return Result{Decision: DecisionAsk, Approvals: []Approval{{
		Reason: reason,
		RuleID: "mcp.tool", Action: action,
	}}}, permit, nil
}

func matchesMCPBinding(state *mcpServiceState, binding llm.ToolBinding, scope string) bool {
	if binding.Operation != "" && binding.Operation != llm.OperationResourceRead {
		return false
	}
	if state == nil || !state.enabled || state.revoked || state.connection != binding.ConnectionFingerprint || state.scope != scope {
		return false
	}
	tool, exists := state.tools[mcpOperationKey(binding.Operation, binding.ToolName)]
	return exists && tool.Allowed && tool.UserDecision != DecisionDeny && tool.SchemaFingerprint == binding.SchemaFingerprint
}

// Validate checks a call approved once as well as a Session-granted call. It
// never creates authority; the Loop remains responsible for the approval itself.
func (p *MCPPermit) Validate(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("guard: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.guard == nil {
		return fmt.Errorf("MCP permission is unavailable")
	}
	p.guard.mu.RLock()
	defer p.guard.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.validateLocked()
}

func (p *MCPPermit) validateLocked() error {
	state := p.guard.mcpServices[p.key]
	if p.session != p.guard.mcpSession || !matchesMCPBinding(state, p.binding, p.scope) ||
		state.epoch != p.epoch || state.tools[mcpOperationKey(p.binding.Operation, p.binding.ToolName)].epoch != p.tool {
		return fmt.Errorf("MCP permission changed after checking; discover and approve the current tool again")
	}
	return nil
}

// AllowSession commits an explicit user choice. A service grant covers only
// this check's eligible tool versions; a changed catalog must be shown again.
func (p *MCPPermit) AllowSession(ctx context.Context, service bool) error {
	if ctx == nil {
		return fmt.Errorf("guard: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.guard == nil {
		return fmt.Errorf("MCP permission is unavailable")
	}
	p.guard.mu.Lock()
	defer p.guard.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.validateLocked(); err != nil {
		return err
	}
	state := p.guard.mcpServices[p.key]
	if service {
		current := make(map[string]uint64)
		for name, tool := range state.tools {
			if tool.Allowed && tool.UserDecision != DecisionDeny {
				current[name] = tool.epoch
			}
		}
		if !maps.Equal(current, p.serviceTools) {
			return fmt.Errorf("MCP service catalog changed during approval; approve the current catalog again")
		}
		maps.Copy(state.granted, current)
	} else {
		state.granted[mcpOperationKey(p.binding.Operation, p.binding.ToolName)] = p.tool
	}
	return nil
}

func validMCPIdentity(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Ordinary names forbid control characters, so this internal key cannot
// collide with a server-chosen tool name. It never enters a message or prompt.
func mcpOperationKey(operation, name string) string {
	if operation == "" {
		return name
	}
	return "\x00" + operation + "/" + name
}
