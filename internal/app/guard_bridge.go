package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// newExecutionGuard constructs the intrinsic gate independently of provider
// credentials so an unauthenticated interactive Session already owns the
// workspace boundary and can preserve it through the first /login.
func newExecutionGuard(
	workspace string,
) (*guard.Guard, *guardAdapter, error) {
	// Built-in guard: intrinsic execution gate, not a plugin. Workspace-scoped
	// so .env relative to the project is correctly recognized. Disabled only
	// when explicitly configured off (future: guard config in settings.json).
	g, err := guard.New(workspace, guard.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("app: create guard: %w", err)
	}
	adapter := &guardAdapter{inner: g}
	return g, adapter, nil
}

// guardAdapter bridges internal/guard.Guard to agent.Guard without making
// agent import guard directly in its core package. It lives in app which
// already depends on both, preserving the "consumer defines interface" rule.
type guardAdapter struct {
	inner *guard.Guard
}

func (g *guardAdapter) Check(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	if g == nil || g.inner == nil {
		return agent.GuardResult{
			Decision: agent.GuardDeny,
			Reason:   "execution gate is not configured",
			RuleID:   "guard.unavailable",
		}, nil
	}
	res, err := g.inner.Check(ctx, call)
	if err != nil {
		return agent.GuardResult{}, err
	}
	return mapGuardResult(res), nil
}

func mapGuardResult(res guard.Result) agent.GuardResult {
	action := agent.GuardAction{
		Kind:     res.Action.Kind,
		Path:     res.Action.Path,
		Command:  res.Action.Command,
		ToolName: res.Action.ToolName,
	}
	switch res.Decision {
	case guard.DecisionAllow:
		return agent.GuardResult{
			Decision: agent.GuardAllow,
			Reason:   res.Reason,
			RuleID:   res.RuleID,
			Action:   action,
		}
	case guard.DecisionDeny:
		return agent.GuardResult{
			Decision: agent.GuardDeny,
			Reason:   res.Reason,
			RuleID:   res.RuleID,
			Action:   action,
		}
	case guard.DecisionAsk:
		return agent.GuardResult{
			Decision: agent.GuardAsk,
			Reason:   res.Reason,
			RuleID:   res.RuleID,
			Action:   action,
		}
	default:
		return agent.GuardResult{
			Decision: agent.GuardDeny,
			Reason:   "execution gate returned an unknown decision",
			RuleID:   "guard.unknown_decision",
			Action:   action,
		}
	}
}

// GuardRequests exposes pending guard confirmations for the TUI.
func (s *interactiveSession) GuardRequests() <-chan interaction.GuardRequest {
	if s == nil {
		return nil
	}
	return s.guardRequests
}

func (s *interactiveSession) handleGuardAsk(ctx context.Context, call llm.ToolCall, result agent.GuardResult) (agent.GuardDecision, error) {
	if s == nil || s.guardRequests == nil {
		return agent.GuardDeny, nil
	}
	// Use a small ID for display; call.ID is the tool-call ID.
	reqID := call.ID
	if reqID == "" {
		reqID = result.RuleID
	}
	reply := make(chan interaction.GuardDecision, 1)
	req := interaction.GuardRequest{
		ID:       reqID,
		ToolName: call.Name,
		Reason:   result.Reason,
		RuleID:   result.RuleID,
		Command:  result.Action.Command,
		Path:     result.Action.Path,
		Reply:    reply,
	}
	select {
	case <-ctx.Done():
		return agent.GuardDeny, ctx.Err()
	case s.guardRequests <- req:
	}
	select {
	case <-ctx.Done():
		return agent.GuardDeny, ctx.Err()
	case decision := <-reply:
		switch decision {
		case interaction.GuardDecisionAllowAlways:
			if s.guard != nil {
				switch {
				case result.RuleID == "pathAccess.ask":
					abs := s.guard.ResolveAbsolute(result.Action.Path, result.Action.ToolName)
					// Allow always grants this path only (isDir=false). It does
					// not authorize the parent directory or sibling files.
					s.guard.AllowPathSession(abs, false)
				case result.RuleID == "permissionGate.dangerous":
					if result.Action.Command != "" {
						s.guard.AllowCommandSession(result.Action.Command)
					} else if result.Action.Path != "" {
						s.guard.AllowPathSession(result.Action.Path, false)
					}
				default:
					if result.Action.Path != "" {
						s.guard.AllowSession(result.Action.Path)
					}
				}
			}
			return agent.GuardAllow, nil
		case interaction.GuardDecisionAllowOnce:
			return agent.GuardAllow, nil
		default:
			return agent.GuardDeny, nil
		}
	}
}
