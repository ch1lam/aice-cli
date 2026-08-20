package guard

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func resolveAbsolute(p, workspace string) string {
	expanded := expandHome(p)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	if workspace != "" {
		return filepath.Clean(filepath.Join(workspace, expanded))
	}
	return filepath.Clean(expanded)
}

// Guard is the built-in execution gate. It is immutable after construction
// except for session-scoped allows which are stored in the in-memory map.
type Guard struct {
	workspace string
	enabled   bool
	policies  []compiledPolicy
	exists    func(path, workspace string) bool
	// sessionAllowed tracks file-policy paths allowed for this run.
	sessionAllowed map[string]bool
	// permission gate
	dangerousPatterns []compiledCommandPattern
	allowedCmdPatterns []compiledCommandPattern
	autoDenyPatterns   []compiledCommandPattern
	// path access
	pathAccessMode     PathAccessMode
	allowedPaths       []AllowedPath
	sessionAllowedPaths map[string]bool // absolute paths or dir: prefix
}

// New constructs a Guard for the given workspace and configuration.
// workspace may be empty (uses raw paths without relative conversion).
func New(workspace string, cfg Config) (*Guard, error) {
	if cfg.Enabled != nil && !*cfg.Enabled {
		return &Guard{workspace: workspace, enabled: false, exists: fileExists, sessionAllowed: map[string]bool{}, sessionAllowedPaths: map[string]bool{}}, nil
	}
	resolved := ResolveConfig(cfg)
	policies := compilePolicies(resolved.Policies)
	return &Guard{
		workspace:            workspace,
		enabled:              resolved.EnabledOrDefault(),
		policies:             policies,
		dangerousPatterns:    compileCommandPatterns(resolved.PermissionGate.Patterns),
		allowedCmdPatterns:   compileCommandPatterns(resolved.PermissionGate.AllowedPatterns),
		autoDenyPatterns:     compileCommandPatterns(resolved.PermissionGate.AutoDenyPatterns),
		pathAccessMode:       resolved.PathAccess.modeOrDefault(),
		allowedPaths:         resolved.PathAccess.AllowedPaths,
		exists:               fileExists,
		sessionAllowed:       map[string]bool{},
		sessionAllowedPaths:  map[string]bool{},
	}, nil
}

// NewWithExists is test-only: injects a custom existence probe.
func NewWithExists(workspace string, cfg Config, exists func(string, string) bool) (*Guard, error) {
	g, err := New(workspace, cfg)
	if err != nil {
		return nil, err
	}
	if exists != nil {
		g.exists = exists
	}
	return g, nil
}

// AllowSession records that path is allowed for the remainder of this run.
// Caller should normalize via normalizeTarget before calling; we do it again
// for safety.
func (g *Guard) AllowSession(path string) {
	if g == nil {
		return
	}
	key := normalizeTarget(path, g.workspace)
	g.sessionAllowed[key] = true
}

// AllowPathSession records a path-access grant for the remainder of this run.
// isDir indicates whether the grant is for the directory and its descendants.
func (g *Guard) AllowPathSession(absPath string, isDir bool) {
	if g == nil {
		return
	}
	if isDir {
		g.sessionAllowedPaths["dir:"+filepath.Clean(absPath)] = true
	} else {
		g.sessionAllowedPaths[filepath.Clean(absPath)] = true
	}
}

// Check evaluates one ToolCall against policies, permission gate, and path
// access. It returns DecisionDeny when blocked. In PR2, Ask is still mapped to
// Deny with a reason mentioning "requires confirmation (no UI)" so
// non-interactive runs fail closed; interactive TUI will map Ask to a prompt in PR3.
func (g *Guard) Check(ctx context.Context, call llm.ToolCall) (Result, error) {
	if g == nil || !g.enabled {
		return Result{Decision: DecisionAllow}, nil
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("guard: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// 1. Permission gate: bash command shape (autoDeny -> deny, dangerous -> deny unless allowed)
	if call.Name == "bash" {
		cmd := extractCommand(call.Arguments)
		if cmd != "" {
			// Auto-deny has highest priority.
			if hit := matchCommandPattern(cmd, g.autoDenyPatterns); hit != nil {
				return Result{Decision: DecisionDeny, Reason: formatCommandBlockReason(hit, ""), RuleID: "permissionGate.autoDeny", Action: Action{Kind: "command", Command: cmd, ToolName: call.Name}}, nil
			}
			// Allowed patterns bypass dangerous check.
			if matchCommandPattern(cmd, g.allowedCmdPatterns) == nil {
				if hit := matchCommandPattern(cmd, g.dangerousPatterns); hit != nil {
					return Result{Decision: DecisionDeny, Reason: formatCommandBlockReason(hit, ""), RuleID: "permissionGate.dangerous", Action: Action{Kind: "command", Command: cmd, ToolName: call.Name}}, nil
				}
			}
		}
	}
	// 2. File policy + path access for extracted file actions
	actions := extractActions(call)
	if len(actions) == 0 {
		return Result{Decision: DecisionAllow}, nil
	}
	for _, act := range actions {
		// Session allow for file policy
		key := normalizeTarget(act.Path, g.workspace)
		if g.sessionAllowed[key] {
			// Still need path-access check below; do not continue entirely
		} else {
			for _, pol := range g.policies {
				if !isBlocked(pol.protection, call.Name) {
					continue
				}
				matched, reason := checkPolicy(ctx, pol, act, g.workspace, g.exists)
				if matched {
					return Result{
						Decision: DecisionDeny,
						Reason:   reason,
						RuleID:   pol.id,
						Action:   act,
					}, nil
				}
			}
		}
		// 3. Path access (outside workspace)
		if g.pathAccessMode == PathAccessAllow {
			continue
		}
		abs := resolveAbsolute(act.Path, g.workspace)
		if g.workspace != "" && isWithinBoundary(abs, g.workspace) {
			continue
		}
		if isPathAllowed(abs, g.allowedPaths, g.workspace, g.sessionAllowedPaths) {
			continue
		}
		if g.pathAccessMode == PathAccessBlock {
			return Result{Decision: DecisionDeny, Reason: fmt.Sprintf("Access to %s is blocked (outside working directory).", resolveForDisplay(abs, g.workspace)), RuleID: "pathAccess.block", Action: act}, nil
		}
		// Ask mode without UI in PR2: deny with reason. PR3 will surface a prompt.
		return Result{Decision: DecisionDeny, Reason: fmt.Sprintf("Access to %s is blocked (outside working directory, requires confirmation).", resolveForDisplay(abs, g.workspace)), RuleID: "pathAccess.ask", Action: act}, nil
	}
	return Result{Decision: DecisionAllow}, nil
}

// IsEnabled reports whether this guard will enforce policies.
func (g *Guard) IsEnabled() bool { return g != nil && g.enabled }

// String helper for block messages that reference the file.
func formatBlockMessage(tmpl, file string) string {
	return strings.ReplaceAll(tmpl, "{file}", file)
}
