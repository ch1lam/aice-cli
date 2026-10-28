package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// Auto mode rule identifiers.
const (
	ruleAutoBlock       = "auto.block"
	ruleAutoUnavailable = "auto.unavailable"
)

// Fuse limits: consecutive or total classifier blocks since the latest user
// message before a person decides. Headless runs stop through
// agent.RunLimits instead.
const (
	autoBlockStreak = 3
	autoBlockTotal  = 20
)

// humanOnlyRules never go to the classifier: AICE configuration, privilege,
// disk, fetched code, the user's own ask rules, MCP connection approval and
// tools the gate does not know.
var humanOnlyRules = []string{
	guard.RuleControlFile, guard.RuleBashPrivileged, guard.RuleBashDisk, guard.RuleBashFetchExec,
	guard.RuleUserAsk, guardRuleMCPConnection, guardRuleUnknownTool,
}

// classifierFunc judges one request; tests replace the model-backed classifier.
type classifierFunc func(context.Context, classify.Request) (classify.Verdict, error)

// autoClassifier creates the model-backed classifier on first use, so an
// unauthenticated Session can start; a failed setup is retried next time
// and reports Unavailable, which a person resolves.
func (a *application) autoClassifier(configured configuredModel) classifierFunc {
	var mu sync.Mutex
	var classifier *classify.Classifier
	return func(ctx context.Context, req classify.Request) (classify.Verdict, error) {
		mu.Lock()
		if classifier == nil {
			c, err := a.newClassifier(configured)
			if err != nil {
				mu.Unlock()
				return classify.Verdict{Decision: classify.Unavailable, Reason: err.Error()}, ctx.Err()
			}
			classifier = c
		}
		mu.Unlock()
		return classifier.Classify(ctx, req)
	}
}

// newClassifier uses auto_mode.model on the same provider, or the startup
// model.
func (a *application) newClassifier(configured configuredModel) (*classify.Classifier, error) {
	if name := configured.configuration.AutoMode.Model; name != "" && name != configured.model.ID {
		cfg := configured.configuration
		cfg.Model = name
		model, options, err := resolveModelSettings(a.dependencies.providers, cfg)
		if err != nil {
			return nil, fmt.Errorf("auto_mode.model: %w", err)
		}
		configured = configuredModel{configuration: cfg, model: model, options: options}
	}
	if err := a.initializeConfiguredModel(&configured); err != nil {
		return nil, err
	}
	return classify.New(classify.Config{Model: classifierModel{configured: configured}, ModelName: configured.model.ID})
}

// autoRunLimits adds the headless fuse: a print run stops after repeated
// denials instead of asking.
func autoRunLimits(c config.Config) agent.RunLimits {
	limits := runLimits(c)
	if c.PermissionMode == config.PermissionModeAuto {
		limits.GuardDenials = agent.GuardDenialLimit{Consecutive: autoBlockStreak, Total: autoBlockTotal}
	}
	return limits
}

// autoActive reports whether auto mode decides this adapter's calls.
func (g *guardAdapter) autoActive() bool {
	return g != nil && g.auto != nil && g.auto.enabled.Load()
}

// setPermissionMode switches modes for the next check. Auto mode cannot be
// combined with --yolo.
func (g *guardAdapter) setPermissionMode(mode config.PermissionMode) error {
	if g == nil || g.auto == nil {
		return fmt.Errorf("app: permission modes are unavailable")
	}
	auto := mode == config.PermissionModeAuto
	if auto && g.yolo {
		return fmt.Errorf("app: --yolo cannot be combined with permission mode auto")
	}
	g.auto.enabled.Store(auto)
	return nil
}

// autoMode resolves eligible gate decisions with the action classifier. It
// keeps no conversation state: intent, earlier actions and the block streak
// are read from the run's active branch at each check, so switching modes
// takes effect on the next call and a restored Session keeps its history.
type autoMode struct {
	classify  classifierFunc
	workspace string
	slots     classify.Slots
	// headless runs have no person: escalations deny and the Loop's
	// GuardDenials limit stops a blocked run.
	headless bool
	enabled  atomic.Bool
}

func newAutoMode(classifier classifierFunc, workspace string, settings config.AutoModeSettings, headless, enabled bool) *autoMode {
	a := &autoMode{
		classify:  classifier,
		workspace: workspace,
		slots:     classify.Slots{Environment: settings.Environment, Block: settings.Block, Allow: settings.Allow},
		headless:  headless,
	}
	a.enabled.Store(enabled)
	return a
}

// decide routes a default-mode result: denials and human-only approvals
// pass through; eligible asks and unreviewed side effects go to the
// classifier.
func (a *autoMode) decide(ctx context.Context, call llm.ToolCall, res agent.GuardResult, hints routeHints) (agent.GuardResult, error) {
	switch {
	case res.Decision == agent.GuardDeny,
		res.Decision == agent.GuardAsk && slices.ContainsFunc(res.Approvals, func(ap agent.GuardApproval) bool { return slices.Contains(humanOnlyRules, ap.RuleID) }),
		res.Decision == agent.GuardAllow && !a.needsReview(call, hints):
		return res, nil
	}
	entries, err := transcriptFrom(ctx)
	if err != nil {
		return a.escalate(call, res, err.Error()), nil
	}
	req := transcriptRequest(entries, call)
	req.Action = classifierAction(call, res)
	req.Environment = gitEnvironment(a.workspace)
	req.Environment.Slots = a.slots
	verdict, err := a.classify(ctx, req)
	if err != nil {
		return agent.GuardResult{}, err
	}
	review := &llm.ToolReview{Mode: string(config.PermissionModeAuto), Decision: string(verdict.Decision), Stage: verdict.Stage, Reason: verdict.Reason, Classifier: verdict.Classifier}
	if verdict.Usage != (llm.Usage{}) {
		usage := verdict.Usage
		review.Usage = &usage
	}
	switch verdict.Decision {
	case classify.Allow:
		return agent.GuardResult{Decision: agent.GuardAllow, Revalidate: res.Revalidate, Review: review}, nil
	case classify.Block:
		streak, total := blockCounts(entries)
		streak, total = streak+1, total+1
		if !a.headless && (streak >= autoBlockStreak || total >= autoBlockTotal) {
			return agent.GuardResult{Decision: agent.GuardAsk, Revalidate: res.Revalidate, Approvals: []agent.GuardApproval{{
				RuleID: ruleAutoBlock,
				Reason: fmt.Sprintf("Auto mode blocked %d actions in a row; you decide this one. Classifier: %s", streak, verdict.Reason),
				Action: approvalAction(call, res),
			}}}, nil
		}
		return agent.GuardResult{
			Decision: agent.GuardDeny, RuleID: ruleAutoBlock, Review: review,
			Reason: "Auto mode blocked this action: " + strings.TrimRight(verdict.Reason, ". ") + ". Do not retry it or work around the block; take a safer approach, or stop and ask the user.",
		}, nil
	}
	return a.escalate(call, res, verdict.Reason), nil
}

// escalate gives a call without a judgement to a person, keeping the gate's
// approvals when it had any. Headless runs deny it.
func (a *autoMode) escalate(call llm.ToolCall, res agent.GuardResult, reason string) agent.GuardResult {
	approvals := slices.Clone(res.Approvals)
	if len(approvals) == 0 {
		approvals = []agent.GuardApproval{{RuleID: ruleAutoUnavailable, Action: approvalAction(call, res)}}
	}
	for i := range approvals {
		approvals[i].Reason = strings.TrimSpace(approvals[i].Reason + " Auto mode could not review it: " + reason)
	}
	return agent.GuardResult{Decision: agent.GuardAsk, Revalidate: res.Revalidate, Approvals: approvals}
}

// transcriptRequest derives the classifier's view of the conversation: the
// text the user typed and earlier tool calls with their outcome, never tool
// output, assistant text or compaction summaries.
func transcriptRequest(entries []session.MessageEntry, pending llm.ToolCall) classify.Request {
	var req classify.Request
	outcomes := map[string]string{}
	for _, entry := range entries {
		if result, ok := entry.Message.(llm.ToolResultMessage); ok {
			outcomes[result.ToolCallID] = toolOutcome(result)
		}
	}
	for _, entry := range entries {
		switch message := entry.Message.(type) {
		case llm.UserMessage:
			if text := typedText(message); text != "" {
				req.Intent = append(req.Intent, classify.UserTurn{Text: text})
			}
		case llm.AssistantMessage:
			for _, part := range message.Content {
				call := part.ToolCall
				if part.Type != llm.ContentTypeToolCall || call == nil || call.ID == pending.ID {
					continue
				}
				if outcome, ok := outcomes[call.ID]; ok {
					req.History = append(req.History, classify.PastAction{Tool: call.Name, Arguments: call.Arguments, Outcome: outcome})
				}
			}
		}
	}
	return req
}

// typedText is the user's own words: attachments are appended after them,
// starting at an application marker the attachment cannot move.
func typedText(message llm.UserMessage) string {
	var text strings.Builder
	for _, part := range message.Content {
		if part.Type == llm.ContentTypeText {
			text.WriteString(part.Text)
		}
	}
	typed := text.String()
	for _, marker := range []string{attachedSkillsMarker, attachedFileMarker} {
		if i := strings.Index(typed, marker); i >= 0 {
			typed = typed[:i]
		}
	}
	return strings.TrimSpace(typed)
}

func toolOutcome(result llm.ToolResultMessage) string {
	switch {
	case result.Details != nil && result.Details.Review != nil && result.Details.Review.Decision == string(classify.Block):
		return "blocked"
	case result.IsError && result.Details != nil && result.Details.State == llm.ExecutionNotDispatched:
		return "denied"
	case result.IsError:
		return "failed"
	}
	return "executed"
}

// blockCounts counts classifier blocks since the latest user message: the
// trailing streak and the total.
func blockCounts(entries []session.MessageEntry) (streak, total int) {
	counting := true
	for i := len(entries) - 1; i >= 0; i-- {
		switch message := entries[i].Message.(type) {
		case llm.UserMessage:
			return streak, total
		case llm.ToolResultMessage:
			blocked := toolOutcome(message) == "blocked"
			if blocked {
				total++
			}
			if counting && blocked {
				streak++
			} else {
				counting = false
			}
		}
	}
	return streak, total
}

// needsReview reports allowed calls whose side effects the classifier must
// see: bash that is neither read-only nor covered by an explicit rule, web
// tools, and writes inside .git. MCP calls reach here only after an explicit
// permission or Session grant.
func (a *autoMode) needsReview(call llm.ToolCall, hints routeHints) bool {
	switch {
	case hints.mcp:
		return false
	case call.Name == "bash":
		return !hints.readOnly && !hints.ruleCovered
	case call.Name == "web_fetch" || call.Name == "web_search":
		return true
	case call.Name == "write" || call.Name == "edit":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Arguments, &args) != nil || args.Path == "" {
			return true
		}
		path := args.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(a.workspace, path)
		}
		return hostpath.Within(filepath.Join(a.workspace, ".git"), filepath.Clean(path))
	}
	return false
}

// classifierAction describes the pending call with the effects the gate
// resolved for it.
func classifierAction(call llm.ToolCall, res agent.GuardResult) classify.Action {
	action := classify.Action{Tool: call.Name, Arguments: call.Arguments}
	for _, ap := range res.Approvals {
		action.Reasons = append(action.Reasons, ap.RuleID)
		effect := classify.Effect{Rule: ap.RuleID, Target: ap.Action.Path}
		switch {
		case ap.RuleID == guard.RuleOutsideRead:
			effect.Kind = "read"
		case ap.RuleID == guard.RuleOutsideWrite:
			effect.Kind = "write"
		case ap.RuleID == "mcp.tool":
			effect.Kind, effect.Target = "mcp", ap.Action.Target
		default:
			effect.Kind, effect.Target = "command", ap.Pattern
		}
		action.Effects = append(action.Effects, effect)
	}
	return action
}

func approvalAction(call llm.ToolCall, res agent.GuardResult) agent.GuardAction {
	if len(res.Approvals) > 0 {
		return res.Approvals[0].Action
	}
	action := agent.GuardAction{ToolName: call.Name}
	if call.Name == "bash" {
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(call.Arguments, &args) == nil {
			action.Kind, action.Command = "command", args.Command
		}
	}
	return action
}

// gitEnvironment reads the current branch and remotes from the repository
// files without running git. Missing data stays empty.
func gitEnvironment(workspace string) classify.Environment {
	env := classify.Environment{Workspace: workspace}
	gitDir := filepath.Join(workspace, ".git")
	if data, err := os.ReadFile(gitDir); err == nil {
		// A worktree's .git file points at its git directory.
		if dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workspace, dir)
			}
			gitDir = dir
		}
	}
	if head, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
		env.GitBranch, _ = strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	}
	configPath := filepath.Join(gitDir, "config")
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(data))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		configPath = filepath.Join(common, "config")
	}
	if file, err := os.Open(configPath); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		inRemote := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "[") {
				inRemote = strings.HasPrefix(line, "[remote ")
				continue
			}
			if key, value, ok := strings.Cut(line, "="); ok && inRemote && strings.TrimSpace(key) == "url" && len(env.Remotes) < 8 {
				env.Remotes = append(env.Remotes, strings.TrimSpace(value))
			}
		}
	}
	return env
}

// permissionMode reports the Session's effective mode.
func (s *interactiveSession) permissionMode() string {
	if s.guardAdapter.autoActive() {
		return string(config.PermissionModeAuto)
	}
	return string(config.PermissionModeDefault)
}

// SetPermissionMode switches modes for this Session's next tool call. It is
// not saved; the Settings window changes the saved default.
func (s *interactiveSession) SetPermissionMode(ctx context.Context, mode string) (string, error) {
	if err := ctx.Err(); err != nil {
		return s.permissionMode(), err
	}
	switch config.PermissionMode(mode) {
	case config.PermissionModeDefault, config.PermissionModeAuto:
	default:
		return s.permissionMode(), fmt.Errorf("unknown permission mode %q", mode)
	}
	err := s.guardAdapter.setPermissionMode(config.PermissionMode(mode))
	return s.permissionMode(), err
}
