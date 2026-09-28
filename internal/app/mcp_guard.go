package app

import (
	"context"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// mcpToolBindings is supplied by the run's application-owned catalog. Names
// identify frozen bindings, never authority encoded in a prefix or arguments.
// Closed/stale run bindings return an error, not a fallback to unknownTool.
type mcpToolBindings interface {
	MCPBinding(context.Context, string) (binding llm.ToolBinding, scope string, found bool, err error)
}

func (g *guardAdapter) checkMCP(ctx context.Context, call llm.ToolCall, binding llm.ToolBinding, scope string) (agent.GuardResult, error) {
	result, permit, err := g.inner.CheckMCP(ctx, call.Name, binding, scope)
	if err != nil {
		return agent.GuardResult{}, err
	}
	mapped := mapGuardResult(result)
	if mapped.Decision == agent.GuardDeny {
		return mapped, nil
	}
	revalidate := func(ctx context.Context) error {
		current, currentScope, found, err := g.mcp.MCPBinding(ctx, call.Name)
		if err != nil || !found || current != binding || currentScope != scope {
			return fmt.Errorf("MCP run binding changed after checking")
		}
		return permit.Validate(ctx)
	}
	mapped.Revalidate = revalidate
	for i := range mapped.Approvals {
		mapped.Approvals[i].AllowToolSession = func(ctx context.Context) error {
			if err := revalidate(ctx); err != nil {
				return err
			}
			return permit.AllowSession(ctx, false)
		}
		mapped.Approvals[i].AllowServiceSession = func(ctx context.Context) error {
			if err := revalidate(ctx); err != nil {
				return err
			}
			return permit.AllowSession(ctx, true)
		}
	}
	if g.yolo && mapped.Decision == agent.GuardAsk {
		mapped.Decision = agent.GuardAllow
		mapped.Approvals = nil
	}
	return mapped, nil
}
