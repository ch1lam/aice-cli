package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func (g *guardAdapter) checkGrepPaths(
	ctx context.Context,
	call llm.ToolCall,
	result guard.Result,
) (guard.Result, func(context.Context) error, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return result, nil, err
	}
	if args == nil {
		return result, nil, fmt.Errorf("grep arguments must be an object")
	}
	var input string
	if raw, ok := args["path"]; ok {
		if err := json.Unmarshal(raw, &input); err != nil {
			return result, nil, err
		}
	}
	workspace, err := tool.NewWorkspace(g.inner.Workspace())
	if err != nil {
		return result, nil, err
	}
	normalized, physical, err := workspace.ResolveGrepPaths(input)
	if err != nil {
		return result, nil, fmt.Errorf("resolve grep target: %w", err)
	}
	// The original call has already been checked. Check normalized spelling
	// before the physical target, preserving denials and independent approvals.
	seen := make(map[string]bool)
	for _, path := range []string{normalized, physical} {
		if seen[path] {
			continue
		}
		seen[path] = true
		args["path"], err = json.Marshal(path)
		if err != nil {
			return result, nil, err
		}
		resolvedCall := call
		resolvedCall.Arguments, err = json.Marshal(args)
		if err != nil {
			return result, nil, err
		}
		other, err := g.inner.Check(ctx, resolvedCall)
		if err != nil {
			return result, nil, err
		}
		if other.Decision == guard.DecisionDeny {
			return other, nil, nil
		}
		if other.Decision == guard.DecisionAsk {
			result.Decision = guard.DecisionAsk
			for _, approval := range other.Approvals {
				duplicate := false
				for _, existing := range result.Approvals {
					existingPath := g.inner.ResolveAbsolute(existing.Action.Path, "grep")
					approvalPath := g.inner.ResolveAbsolute(approval.Action.Path, "grep")
					if existing.RuleID == approval.RuleID && existingPath == approvalPath {
						duplicate = true
						break
					}
				}
				if !duplicate {
					result.Approvals = append(result.Approvals, approval)
				}
			}
		}
	}
	revalidate := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		currentName, currentTarget, err := workspace.ResolveGrepPaths(input)
		if err != nil {
			return err
		}
		if currentName != normalized || currentTarget != physical {
			return fmt.Errorf("grep target changed after permission check; retry the call")
		}
		return nil
	}
	return result, revalidate, nil
}
