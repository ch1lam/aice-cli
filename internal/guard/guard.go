package guard

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func resolveAbsolute(p, workspace, toolName string) string {
	expanded := hostpath.ExpandTilde(p)
	if filepath.IsAbs(expanded) || isBashRootedPath(expanded, toolName) {
		return filepath.Clean(expanded)
	}
	if workspace != "" {
		return filepath.Clean(filepath.Join(workspace, expanded))
	}
	return filepath.Clean(expanded)
}

// isBashRootedPath reports whether path is rooted in Bash path syntax. On
// Windows, Git Bash resolves these paths from its filesystem root even though
// filepath.IsAbs reports false without a drive or UNC volume.
func isBashRootedPath(path, toolName string) bool {
	return toolName == "bash" && hostpath.IsBashRooted(path)
}

// Guard is the built-in execution gate. Its policies are fixed; bound
// service state and Session grants are protected by mu.
type Guard struct {
	mu            sync.RWMutex
	workspace     string
	readOnlyRoots []string
	// sessionAllowed tracks secret-file paths allowed for this Session.
	sessionAllowed map[string]bool
	// sessionAllowedTools tracks unknown tool names allowed for this Session.
	sessionAllowedTools map[string]bool
	// sessionCommands are dangerous raw commands granted by exact equality.
	sessionCommands map[string]bool
	// sessionAllowedPaths holds outside-workspace grants: absolute paths or
	// "dir:" prefixed directories.
	sessionAllowedPaths map[string]bool
	// searchTarget is the bound search service fingerprint set by the
	// application per run. An empty value means web_search is unbound.
	searchTarget string
	mcpServices  map[mcpServiceKey]*mcpServiceState
	mcpEpoch     uint64
	mcpSession   uint64
}

// New constructs a Guard for the given workspace and configuration.
// workspace may be empty (uses raw paths without relative conversion).
func New(workspace string, cfg Config) (*Guard, error) {
	return &Guard{
		workspace:           workspace,
		readOnlyRoots:       resolveReadOnlyRoots(cfg.ReadOnlyRoots, workspace),
		sessionAllowed:      map[string]bool{},
		sessionAllowedTools: map[string]bool{},
		sessionCommands:     map[string]bool{},
		sessionAllowedPaths: map[string]bool{},
	}, nil
}

// AllowSession records that path is allowed for the remainder of this session.
// Caller should normalize via normalizeTarget before calling; we do it again
// for safety.
func (g *Guard) AllowSession(path string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := normalizeTarget(path, g.workspace)
	g.sessionAllowed[key] = true
}

// AllowPathSession records a path-access grant for the remainder of this session.
// isDir indicates whether the grant is for the directory and its descendants.
// Grants of the filesystem root or the user's home directory are ignored so
// a session-scoped grant ("Allow … for this session") cannot authorize those
// too-broad scopes; later checks stay ask.
func (g *Guard) AllowPathSession(absPath string, isDir bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cleaned := filepath.Clean(absPath)
	if isGrantTooBroad(cleaned) {
		return
	}
	if isDir {
		g.sessionAllowedPaths["dir:"+cleaned] = true
		return
	}
	g.sessionAllowedPaths[cleaned] = true
}

// AllowCommandSession records that a dangerous command is allowed for the
// remainder of this session. Only an identical raw command bypasses the
// dangerous check.
func (g *Guard) AllowCommandSession(command string) {
	if g == nil || command == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionCommands[command] = true
}

// AllowToolSession records that an unknown tool name is allowed for the
// remainder of this session.
func (g *Guard) AllowToolSession(name string) {
	if g == nil || name == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionAllowedTools[name] = true
}

// ResetSessionGrants removes only in-memory authorizations for the previous
// Session. Configuration and workspace boundaries remain unchanged. Callers
// must stop active work before resetting the Session. Checks and grants are concurrent-safe.
func (g *Guard) ResetSessionGrants() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(g.sessionAllowed)
	clear(g.sessionAllowedTools)
	clear(g.sessionAllowedPaths)
	clear(g.sessionCommands)
	g.mcpSession++
	for _, service := range g.mcpServices {
		clear(service.granted)
	}
}

// ResolveAbsolute exposes resolveAbsolute for callers that need to map a
// user-granted path to the absolute form stored in session grants.
func (g *Guard) ResolveAbsolute(p, toolName string) string {
	return resolveAbsolute(p, g.workspace, toolName)
}

// Workspace returns the workspace this guard is scoped to.
func (g *Guard) Workspace() string { return g.workspace }

// Check evaluates one ToolCall against the secret-file policy, dangerous
// commands and outside-workspace access. It returns DecisionDeny for hard
// blocks and DecisionAsk when user confirmation is required. Non-interactive
// callers treat Ask as Deny (fail-closed); the interactive TUI confirms every
// approval in an Ask. Pending asks never short-circuit checks that could deny.
func (g *Guard) Check(ctx context.Context, call llm.ToolCall) (Result, error) {
	if g == nil {
		return Result{Decision: DecisionAllow}, nil
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("guard: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	// Known tools with no extractable path/command still allow. Completely
	// unknown names cannot be mapped to actions and must not be silent-allow,
	// unless the user granted this tool for the remainder of the session.
	if !isKnownTool(call.Name) {
		if g.sessionAllowedTools[call.Name] {
			return Result{Decision: DecisionAllow}, nil
		}
		return Result{Decision: DecisionAsk, Approvals: []Approval{{
			Reason: fmt.Sprintf("tool %q is not recognized by the execution gate", call.Name),
			RuleID: "unknownTool", Action: Action{ToolName: call.Name},
		}}}, nil
	}
	if isNetworkTool(call.Name) {
		return g.checkNetwork(call), nil
	}
	var approvals []Approval
	if call.Name == "bash" {
		if approval, ok := g.checkDangerousCommand(extractCommand(call.Arguments)); ok {
			approvals = append(approvals, approval)
		}
	}
	seenPaths := make(map[string]bool)
	for _, act := range extractActions(call) {
		if secretFileTools[call.Name] && !g.sessionAllowed[normalizeTarget(act.Path, g.workspace)] {
			if reason, denied := checkSecretFile(ctx, act, g.workspace); denied {
				return Result{Decision: DecisionDeny, Reason: reason, RuleID: secretFilesRuleID, Action: act}, nil
			}
		}
		abs := resolveAbsolute(act.Path, g.workspace, act.ToolName)
		if g.workspace != "" && hostpath.Within(g.workspace, abs) ||
			isPathAccessReadTool(call.Name) && inReadOnlyRoot(abs, g.readOnlyRoots) ||
			isPathGranted(abs, g.sessionAllowedPaths) || seenPaths[abs] {
			continue
		}
		seenPaths[abs] = true
		approvals = append(approvals, Approval{
			Reason: fmt.Sprintf("Access to %s requires confirmation (outside working directory).", resolveForDisplay(abs, g.workspace)),
			RuleID: "pathAccess.ask", Action: act,
		})
	}
	if len(approvals) > 0 {
		return Result{Decision: DecisionAsk, Approvals: approvals}, nil
	}
	return Result{Decision: DecisionAllow}, nil
}

// checkDangerousCommand asks for a structurally dangerous command unless an
// exact Session grant covers it.
func (g *Guard) checkDangerousCommand(cmd string) (Approval, bool) {
	if cmd == "" || g.sessionCommands[cmd] {
		return Approval{}, false
	}
	desc, pattern := structuralDangerousMatch(cmd)
	if desc == "" {
		return Approval{}, false
	}
	return Approval{
		Reason:  "Dangerous command requires confirmation (" + desc + "): " + pattern,
		RuleID:  "permissionGate.dangerous",
		Action:  Action{Kind: "command", Command: cmd, ToolName: "bash"},
		Pattern: pattern,
	}, true
}
