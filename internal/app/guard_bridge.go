package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

const (
	guardOptionAllowOnce       = "allow-once"
	guardOptionAllowRunFile    = "allow-run-file"
	guardOptionAllowRunDir     = "allow-run-dir"
	guardOptionAllowRunCommand = "allow-run-command"
	guardOptionAllowRunPrefix  = "allow-run-prefix"
	guardOptionAllowRunTool    = "allow-run-tool"
	guardOptionDeny            = "deny"

	guardRulePathAccessAsk = "pathAccess.ask"
	guardRuleDangerous     = "permissionGate.dangerous"
	guardRuleUnknownTool   = "unknownTool"
)

// newExecutionGuard constructs the intrinsic gate independently of provider
// credentials so an unauthenticated interactive Session already owns the
// workspace boundary and can preserve it through the first /login.
func newExecutionGuard(
	workspace string,
	readOnlyRoots []string,
	yolo bool,
) (*guard.Guard, *guardAdapter, error) {
	// Built-in guard: intrinsic execution gate, not a plugin. Workspace-scoped
	// so .env relative to the project is correctly recognized. Disabled only
	// when explicitly configured off (future: guard config in settings.json).
	if physical, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = physical
	}
	g, err := guard.New(workspace, guard.Config{
		ReadOnlyRoots: readOnlyRoots,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("app: create guard: %w", err)
	}
	adapter := &guardAdapter{inner: g, yolo: yolo}
	return g, adapter, nil
}

// guardAdapter bridges internal/guard.Guard to agent.Guard without making
// agent import guard directly in its core package. It lives in app which
// already depends on both, preserving the "consumer defines interface" rule.
type guardAdapter struct {
	inner *guard.Guard
	// yolo upgrades Decision ask to allow. It never remaps deny.
	yolo bool
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
	// Read's tolerant path spelling and symlink resolution must not bypass the
	// gate. Check both the requested name and the physical target before asking.
	if call.Name == "read" && res.Decision != guard.DecisionDeny {
		var args tool.ReadRequest
		if json.Unmarshal(call.Arguments, &args) == nil && args.Path != "" && g.inner.Workspace() != "" {
			workspace, err := tool.NewWorkspace(g.inner.Workspace())
			if err != nil {
				return agent.GuardResult{}, err
			}
			reader, err := tool.NewRead(workspace)
			if err != nil {
				return agent.GuardResult{}, err
			}
			resolved, err := reader.ResolvePath(args.Path)
			if err == nil && resolved != g.inner.ResolveAbsolute(args.Path, "read") {
				args.Path = resolved
				physical := call
				physical.Arguments, _ = json.Marshal(args)
				other, err := g.inner.Check(ctx, physical)
				if err != nil {
					return agent.GuardResult{}, err
				}
				if other.Decision == guard.DecisionDeny {
					res = other
				} else if other.Decision == guard.DecisionAsk {
					res.Decision = guard.DecisionAsk
					res.Approvals = append(res.Approvals, other.Approvals...)
				}
			}
		}
	}
	var revalidate func(context.Context) error
	// Mutations keep literal spelling but follow existing symlinks. Fail closed
	// on resolution errors and check the destination before any approval.
	if (call.Name == "write" || call.Name == "edit") && res.Decision != guard.DecisionDeny {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return agent.GuardResult{}, err
		}
		var path string
		if err := json.Unmarshal(args["path"], &path); err != nil {
			return agent.GuardResult{}, err
		}
		workspace, err := tool.NewWorkspace(g.inner.Workspace())
		if err != nil {
			return agent.GuardResult{}, err
		}
		var resolvePath func(string) (string, error)
		if call.Name == "edit" {
			editor, err := tool.NewEdit(workspace)
			if err != nil {
				return agent.GuardResult{}, err
			}
			resolvePath = editor.ResolvePath
		} else {
			writer, err := tool.NewWrite(workspace)
			if err != nil {
				return agent.GuardResult{}, err
			}
			resolvePath = writer.ResolvePath
		}
		resolved, err := resolvePath(path)
		if err != nil {
			return agent.GuardResult{}, err
		}
		revalidate = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			current, err := resolvePath(path)
			if err != nil {
				return err
			}
			if current != resolved {
				return fmt.Errorf("%s target changed after permission check; retry the call", call.Name)
			}
			return nil
		}
		if resolved != g.inner.ResolveAbsolute(path, call.Name) {
			args["path"], _ = json.Marshal(resolved)
			physical := call
			physical.Arguments, _ = json.Marshal(args)
			other, err := g.inner.Check(ctx, physical)
			if err != nil {
				return agent.GuardResult{}, err
			}
			if other.Decision == guard.DecisionDeny {
				res = other
			} else if other.Decision == guard.DecisionAsk {
				res.Decision = guard.DecisionAsk
				res.Approvals = append(res.Approvals, other.Approvals...)
			}
		}
	}
	mapped := mapGuardResult(res)
	mapped.Revalidate = revalidate
	if g.yolo && mapped.Decision == agent.GuardAsk {
		mapped.Decision = agent.GuardAllow
		mapped.Approvals = nil
	}
	return mapped, nil
}

func mapGuardResult(res guard.Result) agent.GuardResult {
	mapped := agent.GuardResult{
		Decision: agent.GuardDecision(res.Decision),
		Reason:   res.Reason, RuleID: res.RuleID,
		Action: mapGuardAction(res.Action),
	}
	for _, approval := range res.Approvals {
		mapped.Approvals = append(mapped.Approvals, mapGuardApproval(approval))
	}
	switch mapped.Decision {
	case agent.GuardAllow, agent.GuardAsk, agent.GuardDeny:
	default:
		mapped.Decision = agent.GuardDeny
		mapped.Reason = "execution gate returned an unknown decision"
		mapped.RuleID = "guard.unknown_decision"
		mapped.Approvals = nil
	}
	if !mapped.Valid() {
		mapped.Decision = agent.GuardDeny
		mapped.Reason = "execution gate returned an invalid result"
		mapped.RuleID = "guard.invalid_result"
		mapped.Approvals = nil
	}
	return mapped
}

func mapGuardApproval(approval guard.Approval) agent.GuardApproval {
	return agent.GuardApproval{
		Reason: approval.Reason, RuleID: approval.RuleID, Pattern: approval.Pattern,
		Action: mapGuardAction(approval.Action),
	}
}

func mapGuardAction(action guard.Action) agent.GuardAction {
	return agent.GuardAction{Kind: action.Kind, Path: action.Path, Command: action.Command, ToolName: action.ToolName}
}

// GuardRequests exposes pending guard confirmations for the TUI.
func (s *interactiveSession) GuardRequests() <-chan interaction.GuardRequest {
	if s == nil {
		return nil
	}
	return s.guardRequests
}

func (s *interactiveSession) handleGuardAsk(ctx context.Context, call llm.ToolCall, result agent.GuardApproval) (agent.GuardAskReply, error) {
	if s == nil || s.guardRequests == nil {
		return agent.GuardAskReply{Decision: agent.GuardDeny}, nil
	}
	// Use a small ID for display; call.ID is the tool-call ID.
	reqID := call.ID
	if reqID == "" {
		reqID = result.RuleID
	}
	toolName := result.Action.ToolName
	if toolName == "" {
		toolName = call.Name
	}
	options := guardAskOptions(s.guard, toolName, result)
	promptCtx, closePrompt := context.WithCancel(ctx)
	defer closePrompt()
	reply := make(chan interaction.GuardReply, 1)
	req := interaction.GuardRequest{
		ID:        reqID,
		ToolName:  call.Name,
		Reason:    result.Reason,
		RuleID:    result.RuleID,
		Command:   result.Action.Command,
		Path:      result.Action.Path,
		Highlight: result.Pattern,
		Options:   options,
		Reply:     reply,
		Done:      promptCtx.Done(),
	}
	select {
	case <-ctx.Done():
		return agent.GuardAskReply{Decision: agent.GuardDeny}, ctx.Err()
	case s.guardRequests <- req:
	}
	select {
	case <-ctx.Done():
		return agent.GuardAskReply{Decision: agent.GuardDeny}, ctx.Err()
	case got := <-reply:
		// Honor only IDs this prompt actually offered so a reply cannot
		// escalate to a broader grant than the user was shown.
		if !guardOptionOffered(options, got.OptionID) || got.OptionID == guardOptionDeny {
			return agent.GuardAskReply{
				Decision: agent.GuardDeny,
				Feedback: got.Feedback,
			}, nil
		}
		s.applyGuardAskGrant(got.OptionID, toolName, result)
		return agent.GuardAskReply{Decision: agent.GuardAllow}, nil
	}
}

func guardAskOptions(g *guard.Guard, toolName string, result agent.GuardApproval) []interaction.GuardOption {
	switch result.RuleID {
	case guardRulePathAccessAsk:
		if result.Action.Path == "" {
			return guardAskOnceOrDeny()
		}
		return pathAccessAskOptions(g, toolName, result.Action.Path)
	case guardRuleDangerous:
		if result.Action.Command == "" {
			return guardAskOnceOrDeny()
		}
		return dangerousAskOptions(result.Action.Command)
	case guardRuleUnknownTool:
		return []interaction.GuardOption{
			{ID: guardOptionAllowOnce, Label: "Allow once"},
			{
				ID:    guardOptionAllowRunTool,
				Label: fmt.Sprintf("Allow tool %q for this session", toolName),
			},
			{ID: guardOptionDeny, Label: "Deny", Deny: true},
		}
	default:
		return guardAskOnceOrDeny()
	}
}

func pathAccessAskOptions(g *guard.Guard, toolName, path string) []interaction.GuardOption {
	abs := resolveGuardAbs(g, path, toolName)
	options := []interaction.GuardOption{
		{ID: guardOptionAllowOnce, Label: "Allow once"},
		{
			ID:     guardOptionAllowRunFile,
			Label:  "Allow this file for this session",
			Detail: hostpath.HomeDisplay(abs),
		},
	}
	parent := filepath.Dir(abs)
	if !guard.GrantTooBroad(parent) {
		options = append(options, interaction.GuardOption{
			ID:    guardOptionAllowRunDir,
			Label: "Allow directory " + hostpath.HomeDisplay(parent) + "/ for this session",
		})
	}
	return append(options, interaction.GuardOption{
		ID:    guardOptionDeny,
		Label: "Deny",
		Deny:  true,
	})
}

func dangerousAskOptions(command string) []interaction.GuardOption {
	options := []interaction.GuardOption{
		{ID: guardOptionAllowOnce, Label: "Allow once"},
		{ID: guardOptionAllowRunCommand, Label: "Allow this exact command for this session"},
	}
	if prefix := guard.CommandPrefix(command); prefix != "" {
		options = append(options, interaction.GuardOption{
			ID:    guardOptionAllowRunPrefix,
			Label: fmt.Sprintf(`Allow "%s …" commands for this session`, prefix),
		})
	}
	return append(options, interaction.GuardOption{
		ID:    guardOptionDeny,
		Label: "Deny",
		Deny:  true,
	})
}

func guardAskOnceOrDeny() []interaction.GuardOption {
	return []interaction.GuardOption{
		{ID: guardOptionAllowOnce, Label: "Allow once"},
		{ID: guardOptionDeny, Label: "Deny", Deny: true},
	}
}

func (s *interactiveSession) applyGuardAskGrant(optionID, toolName string, result agent.GuardApproval) {
	if s == nil || s.guard == nil {
		return
	}
	g := s.guard
	switch optionID {
	case guardOptionAllowOnce:
		return
	case guardOptionAllowRunFile:
		abs := g.ResolveAbsolute(result.Action.Path, toolName)
		g.AllowPathSession(abs, false)
	case guardOptionAllowRunDir:
		abs := g.ResolveAbsolute(result.Action.Path, toolName)
		g.AllowPathSession(filepath.Dir(abs), true)
	case guardOptionAllowRunCommand:
		g.AllowCommandSession(result.Action.Command)
	case guardOptionAllowRunPrefix:
		prefix := guard.CommandPrefix(result.Action.Command)
		if prefix == "" {
			g.AllowCommandSession(result.Action.Command)
			return
		}
		g.AllowCommandPrefixSession(prefix)
	case guardOptionAllowRunTool:
		g.AllowToolSession(toolName)
	}
}

func resolveGuardAbs(g *guard.Guard, path, toolName string) string {
	if g != nil {
		return g.ResolveAbsolute(path, toolName)
	}
	return filepath.Clean(path)
}

func guardOptionOffered(options []interaction.GuardOption, id string) bool {
	for _, option := range options {
		if option.ID == id {
			return true
		}
	}
	return false
}
