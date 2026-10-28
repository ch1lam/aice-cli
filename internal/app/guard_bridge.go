package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
	"github.com/ch1lam/aice-cli/internal/trust"
)

const (
	guardOptionAllowOnce       = "allow-once"
	guardOptionAllowRunFile    = "allow-run-file"
	guardOptionAllowRunDir     = "allow-run-dir"
	guardOptionAllowRunCommand = "allow-run-command"
	guardOptionAllowRunTool    = "allow-run-tool"
	guardOptionAlwaysDir       = "allow-always-dir"
	guardOptionAlwaysCommand   = "allow-always-command"
	guardOptionAllowMCPTool    = "allow-mcp-tool-session"
	guardOptionAllowMCPService = "allow-mcp-service-session"
	guardOptionDeny            = "deny"
	guardOptionDenyDefaultMode = "deny-default-mode"

	guardRuleUnknownTool = "unknownTool"
)

// newExecutionGuard constructs the intrinsic gate independently of provider
// credentials so an unauthenticated interactive Session already owns the
// workspace boundary and can preserve it through the first /login.
func newExecutionGuard(
	workspace string,
	cfg guard.Config,
	yolo bool,
) (*guard.Guard, *guardAdapter, error) {
	if physical, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = physical
	}
	g, err := guard.New(workspace, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("app: create guard: %w", err)
	}
	adapter := &guardAdapter{inner: g, yolo: yolo}
	return g, adapter, nil
}

// executionGuardConfig names the files AICE owns and the configured rules:
// credential stores are denied, and configuration that changes AICE's own
// authority always asks.
func executionGuardConfig(configuration config.Config, workspace string, readOnlyRoots []string) guard.Config {
	paths := configuration.Paths
	cfg := guard.Config{ReadOnlyRoots: readOnlyRoots, Rules: guard.Rules{
		Allow: configuration.Permissions.Allow,
		Ask:   configuration.Permissions.Ask,
		Deny:  configuration.Permissions.Deny,
	}}
	if paths.GlobalAuth != "" {
		cfg.CredentialFiles = []string{paths.GlobalAuth, config.ClaudeSubscriptionAuthPath(paths), config.CodexAuthPath(paths)}
	}
	cfg.ControlFiles = slices.DeleteFunc([]string{paths.GlobalSettings, paths.GlobalTrust}, func(p string) bool { return p == "" })
	if workspace != "" {
		for _, name := range []string{trust.SettingsFile, trust.SystemFile, trust.AppendSystemFile, trust.MCPFile} {
			cfg.ControlFiles = append(cfg.ControlFiles, filepath.Join(workspace, filepath.FromSlash(name)))
		}
	}
	return cfg
}

// guardAdapter bridges internal/guard.Guard to agent.Guard without making
// agent import guard directly in its core package. It lives in app which
// already depends on both, preserving the "consumer defines interface" rule.
type guardAdapter struct {
	inner *guard.Guard
	mcp   mcpToolBindings
	// auto is set in auto permission mode.
	auto *autoMode
	// yolo upgrades Decision ask to allow. It never remaps deny.
	yolo bool
}

func (g *guardAdapter) Check(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	result, hints, err := g.evaluate(ctx, call)
	if err != nil || !g.autoActive() {
		return result, err
	}
	return g.auto.decide(ctx, call, result, hints)
}

// checkInput checks a user attachment. The user chose the file, so auto mode
// does not classify it; approvals stay with the person.
func (g *guardAdapter) checkInput(ctx context.Context, call llm.ToolCall) (agent.GuardResult, error) {
	result, _, err := g.evaluate(ctx, call)
	return result, err
}

// routeHints tell auto mode how an allowed call was reached.
type routeHints struct {
	readOnly    bool // bash made only of read-only commands
	ruleCovered bool // bash covered by explicit allow rules or an exact grant
	mcp         bool // an MCP binding decided the call
}

// evaluate applies the default-mode gate, including --yolo.
func (g *guardAdapter) evaluate(ctx context.Context, call llm.ToolCall) (agent.GuardResult, routeHints, error) {
	if g == nil || g.inner == nil {
		return agent.GuardResult{
			Decision: agent.GuardDeny,
			Reason:   "execution gate is not configured",
			RuleID:   "guard.unavailable",
		}, routeHints{}, nil
	}
	if g.mcp != nil {
		binding, scope, found, err := g.mcp.MCPBinding(ctx, call.Name)
		if err != nil {
			return agent.GuardResult{}, routeHints{}, fmt.Errorf("MCP run binding is unavailable")
		}
		if found {
			result, err := g.checkMCP(ctx, call, binding, scope)
			return result, routeHints{mcp: true}, err
		}
	}
	res, err := g.inner.Check(ctx, call)
	if err != nil {
		return agent.GuardResult{}, routeHints{}, err
	}
	// Read's tolerant path spelling and symlink resolution must not bypass the
	// gate. Check both the requested name and the physical target before asking.
	if call.Name == "read" && res.Decision != guard.DecisionDeny {
		var args tool.ReadRequest
		if json.Unmarshal(call.Arguments, &args) == nil && args.Path != "" && g.inner.Workspace() != "" {
			workspace, err := tool.NewWorkspace(g.inner.Workspace())
			if err != nil {
				return agent.GuardResult{}, routeHints{}, err
			}
			reader, err := tool.NewRead(workspace)
			if err != nil {
				return agent.GuardResult{}, routeHints{}, err
			}
			resolved, err := reader.ResolvePath(args.Path)
			if err == nil && resolved != g.inner.ResolveAbsolute(args.Path, "read") {
				args.Path = resolved
				physical := call
				physical.Arguments, _ = json.Marshal(args)
				other, err := g.inner.Check(ctx, physical)
				if err != nil {
					return agent.GuardResult{}, routeHints{}, err
				}
				if other.Decision == guard.DecisionDeny {
					res = other
				} else if other.Decision == guard.DecisionAsk {
					res.Decision = guard.DecisionAsk
					res.Approvals = appendUniqueApprovals(g.inner, res.Approvals, other.Approvals)
				}
			}
		}
	}
	var revalidate func(context.Context) error
	if call.Name == "grep" && res.Decision != guard.DecisionDeny {
		res, revalidate, err = g.checkGrepPaths(ctx, call, res)
		if err != nil {
			return agent.GuardResult{}, routeHints{}, err
		}
	}
	// Mutations keep literal spelling but follow existing symlinks. Fail closed
	// on resolution errors and check the destination before any approval.
	if (call.Name == "write" || call.Name == "edit") && res.Decision != guard.DecisionDeny {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return agent.GuardResult{}, routeHints{}, err
		}
		var path string
		if err := json.Unmarshal(args["path"], &path); err != nil {
			return agent.GuardResult{}, routeHints{}, err
		}
		workspace, err := tool.NewWorkspace(g.inner.Workspace())
		if err != nil {
			return agent.GuardResult{}, routeHints{}, err
		}
		var resolvePath func(string) (string, error)
		if call.Name == "edit" {
			editor, err := tool.NewEdit(workspace)
			if err != nil {
				return agent.GuardResult{}, routeHints{}, err
			}
			resolvePath = editor.ResolvePath
		} else {
			writer, err := tool.NewWrite(workspace)
			if err != nil {
				return agent.GuardResult{}, routeHints{}, err
			}
			resolvePath = writer.ResolvePath
		}
		resolved, err := resolvePath(path)
		if err != nil {
			return agent.GuardResult{}, routeHints{}, err
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
				return agent.GuardResult{}, routeHints{}, err
			}
			if other.Decision == guard.DecisionDeny {
				res = other
			} else if other.Decision == guard.DecisionAsk {
				res.Decision = guard.DecisionAsk
				res.Approvals = appendUniqueApprovals(g.inner, res.Approvals, other.Approvals)
			}
		}
	}
	mapped := mapGuardResult(res)
	mapped.Revalidate = revalidate
	if g.yolo && mapped.Decision == agent.GuardAsk {
		// Changing AICE's own configuration could lift a user deny, so it
		// still needs a person: the prompt stays, and --print denies.
		mapped.Approvals = slices.DeleteFunc(mapped.Approvals, func(a agent.GuardApproval) bool { return a.RuleID != guard.RuleControlFile })
		if len(mapped.Approvals) == 0 {
			mapped.Decision = agent.GuardAllow
		}
	}
	return mapped, routeHints{readOnly: res.ReadOnly, ruleCovered: res.RuleCovered}, nil
}

// appendUniqueApprovals adds approvals whose rule and resolved path are not
// already listed; the gate may already have checked a physical target.
func appendUniqueApprovals(g *guard.Guard, approvals, more []guard.Approval) []guard.Approval {
	for _, approval := range more {
		duplicate := slices.ContainsFunc(approvals, func(existing guard.Approval) bool {
			return existing.RuleID == approval.RuleID && existing.Action.Command == approval.Action.Command &&
				g.ResolveAbsolute(existing.Action.Path, existing.Action.ToolName) == g.ResolveAbsolute(approval.Action.Path, approval.Action.ToolName)
		})
		if !duplicate {
			approvals = append(approvals, approval)
		}
	}
	return approvals
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
	return agent.GuardAction{Kind: action.Kind, Path: action.Path, Command: action.Command, ToolName: action.ToolName, Target: action.Target}
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
		if got.OptionID == guardOptionDenyDefaultMode && guardOptionOffered(options, got.OptionID) {
			if err := s.guardAdapter.setPermissionMode(config.PermissionModeDefault); err != nil {
				return agent.GuardAskReply{Decision: agent.GuardDeny}, err
			}
			return agent.GuardAskReply{Decision: agent.GuardDeny, Feedback: strings.TrimSpace(got.Feedback + " The user switched to default permission mode; each further action of this kind needs their confirmation.")}, nil
		}
		if !guardOptionOffered(options, got.OptionID) || got.OptionID == guardOptionDeny {
			return agent.GuardAskReply{
				Decision: agent.GuardDeny,
				Feedback: got.Feedback,
			}, nil
		}
		if err := ctx.Err(); err != nil {
			return agent.GuardAskReply{Decision: agent.GuardDeny}, err
		}
		switch got.OptionID {
		case guardOptionAllowMCPTool:
			if err := result.AllowToolSession(ctx); err != nil {
				return agent.GuardAskReply{Decision: agent.GuardDeny}, err
			}
		case guardOptionAllowMCPService:
			if err := result.AllowServiceSession(ctx); err != nil {
				return agent.GuardAskReply{Decision: agent.GuardDeny}, err
			}
		case guardOptionAlwaysDir, guardOptionAlwaysCommand:
			if err := s.saveGuardRule(ctx, got.OptionID, toolName, result); err != nil {
				return agent.GuardAskReply{Decision: agent.GuardDeny}, err
			}
		default:
			s.applyGuardAskGrant(got.OptionID, toolName, result)
		}
		return agent.GuardAskReply{Decision: agent.GuardAllow}, nil
	}
}

func guardAskOptions(g *guard.Guard, toolName string, result agent.GuardApproval) []interaction.GuardOption {
	switch result.RuleID {
	case "mcp.tool":
		options := []interaction.GuardOption{{ID: guardOptionAllowOnce, Label: "Allow once"}}
		if result.AllowToolSession != nil {
			options = append(options, interaction.GuardOption{
				ID: guardOptionAllowMCPTool, Label: "Allow this MCP tool version for this session", Detail: result.Action.Target,
			})
		}
		if result.AllowServiceSession != nil {
			options = append(options, interaction.GuardOption{
				ID: guardOptionAllowMCPService, Label: "Allow this service's current tool versions for this session", Detail: result.Action.Target,
			})
		}
		return append(options, interaction.GuardOption{ID: guardOptionDeny, Label: "Deny", Deny: true})
	case guard.RuleOutsideRead, guard.RuleOutsideWrite:
		if result.Action.Path == "" {
			return guardAskOnceOrDeny()
		}
		return pathAccessAskOptions(g, toolName, result.Action.Path, result.RuleID == guard.RuleOutsideWrite)
	case ruleAutoBlock:
		return []interaction.GuardOption{
			{ID: guardOptionAllowOnce, Label: "Allow once"},
			{ID: guardOptionDeny, Label: "Deny", Deny: true},
			{ID: guardOptionDenyDefaultMode, Label: "Deny and switch to default mode", Deny: true},
		}
	case guard.RuleUserAsk:
		// An ask rule is the user's request to confirm; never offer to save it away.
		if result.Action.Command != "" {
			return commandAskOptions(g, "")
		}
		return guardAskOnceOrDeny()
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
		if guard.IsBashRule(result.RuleID) && result.Action.Command != "" {
			return commandAskOptions(g, result.Action.Command)
		}
		return guardAskOnceOrDeny()
	}
}

func pathAccessAskOptions(g *guard.Guard, toolName, path string, write bool) []interaction.GuardOption {
	abs := resolveGuardAbs(g, path, toolName)
	verb := "reading"
	if write {
		verb = "writing"
	}
	options := []interaction.GuardOption{
		{ID: guardOptionAllowOnce, Label: "Allow once"},
		{
			ID:     guardOptionAllowRunFile,
			Label:  "Allow " + verb + " this file for this session",
			Detail: hostpath.HomeDisplay(abs),
		},
	}
	parent := filepath.Dir(abs)
	if !guard.GrantTooBroad(parent) {
		options = append(options, interaction.GuardOption{
			ID:    guardOptionAllowRunDir,
			Label: "Allow " + verb + " directory " + hostpath.HomeDisplay(parent) + "/ for this session",
		})
	}
	if rule, ok := guard.SuggestPathRule(parent, write); ok {
		options = append(options, interaction.GuardOption{
			ID:     guardOptionAlwaysDir,
			Label:  "Always allow " + verb + " directory " + hostpath.HomeDisplay(parent) + "/",
			Detail: "saves " + rule + " to user settings",
		})
	}
	return append(options, interaction.GuardOption{
		ID:    guardOptionDeny,
		Label: "Deny",
		Deny:  true,
	})
}

// commandAskOptions offers a saved prefix rule only when one prefix covers
// every finding of command.
func commandAskOptions(g *guard.Guard, command string) []interaction.GuardOption {
	options := []interaction.GuardOption{
		{ID: guardOptionAllowOnce, Label: "Allow once"},
		{ID: guardOptionAllowRunCommand, Label: "Allow this exact command for this session"},
	}
	if rule, ok := g.SuggestCommandRule(command); ok {
		prefix := strings.TrimSuffix(strings.TrimPrefix(rule, "bash("), " *)")
		options = append(options, interaction.GuardOption{
			ID:     guardOptionAlwaysCommand,
			Label:  "Always allow `" + prefix + " …` commands",
			Detail: "saves " + rule + " to user settings",
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
		g.AllowPathSession(abs, false, result.RuleID == guard.RuleOutsideWrite)
	case guardOptionAllowRunDir:
		abs := g.ResolveAbsolute(result.Action.Path, toolName)
		g.AllowPathSession(filepath.Dir(abs), true, result.RuleID == guard.RuleOutsideWrite)
	case guardOptionAllowRunCommand:
		g.AllowCommandSession(result.Action.Command)
	case guardOptionAllowRunTool:
		g.AllowToolSession(toolName)
	}
}

// saveGuardRule persists the rule an "always" option showed, then applies
// it to this process. A failed save allows nothing.
func (s *interactiveSession) saveGuardRule(ctx context.Context, optionID, toolName string, result agent.GuardApproval) error {
	g := s.guard
	var rule string
	var ok bool
	if optionID == guardOptionAlwaysDir {
		rule, ok = guard.SuggestPathRule(filepath.Dir(g.ResolveAbsolute(result.Action.Path, toolName)), result.RuleID == guard.RuleOutsideWrite)
	} else {
		rule, ok = g.SuggestCommandRule(result.Action.Command)
	}
	if !ok {
		return fmt.Errorf("permission rule is no longer available")
	}
	if err := config.SavePermissionRule(ctx, s.configuration.Paths, guard.RuleDecisionAllow, rule); err != nil {
		return err
	}
	return g.AddRule(guard.RuleDecisionAllow, rule)
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
