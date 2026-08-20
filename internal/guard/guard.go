package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Guard is the built-in execution gate. It is immutable after construction
// except for session-scoped allows which are stored in the in-memory map.
type Guard struct {
	workspace string
	enabled   bool
	policies  []compiledPolicy
	exists    func(path, workspace string) bool
	// sessionAllowed tracks paths that were allowed once for this run.
	sessionAllowed map[string]bool
}

// New constructs a Guard for the given workspace and configuration.
// workspace may be empty (uses raw paths without relative conversion).
func New(workspace string, cfg Config) (*Guard, error) {
	if cfg.Enabled != nil && !*cfg.Enabled {
		return &Guard{workspace: workspace, enabled: false, exists: fileExists, sessionAllowed: map[string]bool{}}, nil
	}
	resolved := ResolveConfig(cfg)
	policies := compilePolicies(resolved.Policies)
	return &Guard{
		workspace:      workspace,
		enabled:        resolved.EnabledOrDefault(),
		policies:       policies,
		exists:         fileExists,
		sessionAllowed: map[string]bool{},
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

// Check evaluates one ToolCall against the compiled policies.
// It returns DecisionAllow when no policy applies or the tool is not gated
// by that protection, DecisionDeny when blocked, and never returns Ask in PR1
// (Ask is reserved for interactive confirmations in later PRs).
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
	actions := extractActions(call)
	if len(actions) == 0 {
		return Result{Decision: DecisionAllow}, nil
	}
	for _, act := range actions {
		// Session allow bypasses all policies for this normalized path.
		key := normalizeTarget(act.Path, g.workspace)
		if g.sessionAllowed[key] {
			continue
		}
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
	return Result{Decision: DecisionAllow}, nil
}

// IsEnabled reports whether this guard will enforce policies.
func (g *Guard) IsEnabled() bool { return g != nil && g.enabled }

// String helper for block messages that reference the file.
func formatBlockMessage(tmpl, file string) string {
	return strings.ReplaceAll(tmpl, "{file}", file)
}
