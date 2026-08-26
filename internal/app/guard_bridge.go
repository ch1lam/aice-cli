package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
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
	mapped := agent.GuardResult{
		Reason:  res.Reason,
		RuleID:  res.RuleID,
		Pattern: res.Pattern,
		Action: agent.GuardAction{
			Kind:     res.Action.Kind,
			Path:     res.Action.Path,
			Command:  res.Action.Command,
			ToolName: res.Action.ToolName,
		},
	}
	switch res.Decision {
	case guard.DecisionAllow:
		mapped.Decision = agent.GuardAllow
	case guard.DecisionDeny:
		mapped.Decision = agent.GuardDeny
	case guard.DecisionAsk:
		mapped.Decision = agent.GuardAsk
	default:
		mapped.Decision = agent.GuardDeny
		mapped.Reason = "execution gate returned an unknown decision"
		mapped.RuleID = "guard.unknown_decision"
	}
	return mapped
}

// GuardRequests exposes pending guard confirmations for the TUI.
func (s *interactiveSession) GuardRequests() <-chan interaction.GuardRequest {
	if s == nil {
		return nil
	}
	return s.guardRequests
}

func (s *interactiveSession) handleGuardAsk(ctx context.Context, call llm.ToolCall, result agent.GuardResult) (agent.GuardAskReply, error) {
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

func guardAskOptions(g *guard.Guard, toolName string, result agent.GuardResult) []interaction.GuardOption {
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
				Label: fmt.Sprintf("Allow tool %q for this run", toolName),
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
			Label:  "Allow this file for this run",
			Detail: hostpath.HomeDisplay(abs),
		},
	}
	parent := filepath.Dir(abs)
	if !guard.GrantTooBroad(parent) {
		options = append(options, interaction.GuardOption{
			ID:    guardOptionAllowRunDir,
			Label: "Allow directory " + hostpath.HomeDisplay(parent) + "/ for this run",
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
		{ID: guardOptionAllowRunCommand, Label: "Allow this exact command for this run"},
	}
	if prefix := guard.CommandPrefix(command); prefix != "" {
		options = append(options, interaction.GuardOption{
			ID:    guardOptionAllowRunPrefix,
			Label: fmt.Sprintf(`Allow "%s …" commands for this run`, prefix),
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

func (s *interactiveSession) applyGuardAskGrant(optionID, toolName string, result agent.GuardResult) {
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
