package guard

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func resolveAbsolute(p, workspace, toolName string) string {
	expanded := expandHome(p)
	if filepath.IsAbs(expanded) ||
		(runtime.GOOS == "windows" && isBashRootedPath(expanded, toolName)) {
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
	return toolName == "bash" && strings.HasPrefix(path, "/")
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
	// sessionAllowedTools tracks unknown tool names allowed for this run.
	sessionAllowedTools map[string]bool
	// permission gate
	dangerousPatterns    []compiledCommandPattern
	allowedCmdPatterns   []compiledCommandPattern
	sessionCmdPrefixes   []string
	autoDenyPatterns     []compiledCommandPattern
	useBuiltinStructural bool
	requireConfirmation  bool
	// path access
	pathAccessMode      PathAccessMode
	allowedPaths        []AllowedPath
	sessionAllowedPaths map[string]bool // absolute paths or dir: prefix
}

// New constructs a Guard for the given workspace and configuration.
// workspace may be empty (uses raw paths without relative conversion).
func New(workspace string, cfg Config) (*Guard, error) {
	if cfg.Enabled != nil && !*cfg.Enabled {
		return &Guard{
			workspace:           workspace,
			enabled:             false,
			requireConfirmation: true,
			exists:              fileExists,
			sessionAllowed:      map[string]bool{},
			sessionAllowedPaths: map[string]bool{},
			sessionAllowedTools: map[string]bool{},
		}, nil
	}
	resolved := ResolveConfig(cfg)
	policies := compilePolicies(resolved.Policies)
	useBuiltin := len(resolved.PermissionGate.CustomPatterns) == 0
	// When ApplyBuiltinDefaults is false and no custom, dangerousPatterns will be empty; structural should also be off.
	if !resolved.ShouldApplyBuiltins() && len(resolved.PermissionGate.CustomPatterns) == 0 && len(resolved.PermissionGate.Patterns) == 0 {
		useBuiltin = false
	}
	requireConfirm := true
	if resolved.PermissionGate.RequireConfirmation != nil {
		requireConfirm = *resolved.PermissionGate.RequireConfirmation
	}
	return &Guard{
		workspace:            workspace,
		enabled:              resolved.EnabledOrDefault(),
		policies:             policies,
		dangerousPatterns:    compileCommandPatterns(resolved.PermissionGate.Patterns),
		allowedCmdPatterns:   compileCommandPatterns(resolved.PermissionGate.AllowedPatterns),
		autoDenyPatterns:     compileCommandPatterns(resolved.PermissionGate.AutoDenyPatterns),
		useBuiltinStructural: useBuiltin,
		requireConfirmation:  requireConfirm,
		pathAccessMode:       resolved.PathAccess.modeOrDefault(),
		allowedPaths:         resolved.PathAccess.AllowedPaths,
		exists:               fileExists,
		sessionAllowed:       map[string]bool{},
		sessionAllowedPaths:  map[string]bool{},
		sessionAllowedTools:  map[string]bool{},
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
// Grants of the filesystem root or the user's home directory are ignored so
// a run-scoped grant ("Allow … for this run") cannot authorize those
// too-broad scopes; later checks stay ask.
func (g *Guard) AllowPathSession(absPath string, isDir bool) {
	if g == nil {
		return
	}
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
// remainder of this run. It appends the command substring as an allowed
// pattern so future identical commands bypass the dangerous check.
func (g *Guard) AllowCommandSession(command string) {
	if g == nil || command == "" {
		return
	}
	g.allowedCmdPatterns = append(g.allowedCmdPatterns, compileCommandPattern(PatternConfig{Pattern: command}))
}

// AllowToolSession records that an unknown tool name is allowed for the
// remainder of this run.
func (g *Guard) AllowToolSession(name string) {
	if g == nil || name == "" {
		return
	}
	g.sessionAllowedTools[name] = true
}

// AllowCommandPrefixSession records a command-prefix grant for the remainder
// of this run. Future bash commands whose every parsed subcommand matches the
// prefix (exact or prefix plus a following word) skip the dangerous check.
func (g *Guard) AllowCommandPrefixSession(prefix string) {
	if g == nil {
		return
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return
	}
	g.sessionCmdPrefixes = append(g.sessionCmdPrefixes, prefix)
}

// ResolveAbsolute exposes resolveAbsolute for callers that need to map a
// user-granted path to the absolute form stored in session grants.
func (g *Guard) ResolveAbsolute(p, toolName string) string {
	return resolveAbsolute(p, g.workspace, toolName)
}

// Workspace returns the workspace this guard is scoped to.
func (g *Guard) Workspace() string { return g.workspace }

// Check evaluates one ToolCall against policies, permission gate, and path
// access. It returns DecisionDeny for hard blocks and DecisionAsk when user
// confirmation is required. Non-interactive callers treat Ask as Deny
// (fail-closed); the interactive TUI maps Ask to a confirmation prompt.
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
	// Known tools with no extractable path/command still allow. Completely
	// unknown names cannot be mapped to actions and must not be silent-allow,
	// unless the user granted this tool for the remainder of the run.
	if !isKnownTool(call.Name) {
		if g.sessionAllowedTools[call.Name] {
			return Result{Decision: DecisionAllow}, nil
		}
		return Result{
			Decision: DecisionAsk,
			Reason:   fmt.Sprintf("tool %q is not recognized by the execution gate", call.Name),
			RuleID:   "unknownTool",
			Action:   Action{ToolName: call.Name},
		}, nil
	}
	// 1. Permission gate: bash command shape (autoDeny -> deny, structural dangerous -> deny unless allowed)
	if call.Name == "bash" {
		cmd := extractCommand(call.Arguments)
		if cmd != "" {
			// Auto-deny has highest priority.
			if hit := matchCommandPattern(cmd, g.autoDenyPatterns); hit != nil {
				return Result{Decision: DecisionDeny, Reason: formatCommandBlockReason(hit, ""), RuleID: "permissionGate.autoDeny", Action: Action{Kind: "command", Command: cmd, ToolName: call.Name}}, nil
			}
			// Allowed patterns and session command prefixes bypass the dangerous check.
			if matchCommandPattern(cmd, g.allowedCmdPatterns) == nil && !commandCoveredByPrefixes(cmd, g.sessionCmdPrefixes) {
				if g.useBuiltinStructural {
					if desc, pat := structuralDangerousMatch(cmd); desc != "" {
						if g.requireConfirmation {
							return Result{
								Decision: DecisionAsk,
								Reason:   "Dangerous command requires confirmation (" + desc + "): " + pat,
								RuleID:   "permissionGate.dangerous",
								Action:   Action{Kind: "command", Command: cmd, ToolName: call.Name},
								Pattern:  pat,
							}, nil
						}
					}
					// When builtins are active, structural is authoritative; do not fall back to substring builtins
					// to avoid false positives like `echo rm -rf`.
				} else {
					// Custom patterns replace builtins: check substring/regex
					if hit := matchCommandPattern(cmd, g.dangerousPatterns); hit != nil {
						if g.requireConfirmation {
							return Result{
								Decision: DecisionAsk,
								Reason:   formatCommandBlockReason(hit, ""),
								RuleID:   "permissionGate.dangerous",
								Action:   Action{Kind: "command", Command: cmd, ToolName: call.Name},
								Pattern:  hit.source.Pattern,
							}, nil
						}
					}
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
		abs := resolveAbsolute(act.Path, g.workspace, act.ToolName)
		if g.workspace != "" && isWithinBoundary(abs, g.workspace) {
			continue
		}
		if isPathAllowed(abs, g.allowedPaths, g.workspace, g.sessionAllowedPaths) {
			continue
		}
		if g.pathAccessMode == PathAccessBlock {
			return Result{Decision: DecisionDeny, Reason: fmt.Sprintf("Access to %s is blocked (outside working directory).", resolveForDisplay(abs, g.workspace)), RuleID: "pathAccess.block", Action: act}, nil
		}
		return Result{Decision: DecisionAsk, Reason: fmt.Sprintf("Access to %s requires confirmation (outside working directory).", resolveForDisplay(abs, g.workspace)), RuleID: "pathAccess.ask", Action: act}, nil
	}
	return Result{Decision: DecisionAllow}, nil
}

// IsEnabled reports whether this guard will enforce policies.
func (g *Guard) IsEnabled() bool { return g != nil && g.enabled }
