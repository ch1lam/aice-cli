package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/classify"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Auto mode rule identifiers.
const (
	ruleAutoBlock       = "auto.block"
	ruleAutoUnavailable = "auto.unavailable"
)

// Fuse limits: consecutive or total classifier blocks per user turn before
// a person decides. Headless runs stop through agent.RunLimits instead.
const (
	autoBlockStreak = 3
	autoBlockTotal  = 20
	autoHistoryCap  = 200
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

// autoState returns the adapter's auto mode, or nil.
func (g *guardAdapter) autoState() *autoMode {
	if g == nil {
		return nil
	}
	return g.auto
}

// autoMode resolves eligible gate decisions with the action classifier. Its
// state belongs to one interactive Session or print run.
type autoMode struct {
	classify  classifierFunc
	workspace string
	slots     classify.Slots
	// headless runs have no person: escalations deny and the Loop's
	// GuardDenials limit stops a blocked run.
	headless bool

	mu        sync.Mutex
	intent    []classify.UserTurn
	intentVer int
	history   []classify.PastAction
	cache     map[string]classify.Verdict
	streak    int
	total     int
}

func newAutoMode(classifier classifierFunc, workspace string, settings config.AutoModeSettings, headless bool) *autoMode {
	return &autoMode{
		classify:  classifier,
		workspace: workspace,
		slots:     classify.Slots{Environment: settings.Environment, Block: settings.Block, Allow: settings.Allow},
		headless:  headless,
		cache:     map[string]classify.Verdict{},
	}
}

// recordIntent stores text the user typed, before attachments are inlined.
// A new prompt starts a fresh fuse budget; steering resets the streak.
func (a *autoMode) recordIntent(text, kind string) {
	if a == nil || strings.TrimSpace(text) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.intent = append(a.intent, classify.UserTurn{Text: text, Kind: kind})
	a.intentVer++
	clear(a.cache)
	a.streak = 0
	if kind == "prompt" {
		a.total = 0
	}
}

// reset forgets the Session's intent, history, cache and fuse counts.
func (a *autoMode) reset() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.intent, a.history = nil, nil
	a.intentVer++
	clear(a.cache)
	a.streak, a.total = 0, 0
}

// decide routes a default-mode result: denials and human-only approvals
// pass through; eligible asks and unreviewed side effects go to the
// classifier.
func (a *autoMode) decide(ctx context.Context, call llm.ToolCall, res agent.GuardResult, hints routeHints) (agent.GuardResult, error) {
	switch {
	case res.Decision == agent.GuardDeny:
		a.remember(call, "denied")
		return res, nil
	case res.Decision == agent.GuardAsk && slices.ContainsFunc(res.Approvals, func(ap agent.GuardApproval) bool { return slices.Contains(humanOnlyRules, ap.RuleID) }):
		a.remember(call, "asked")
		return res, nil
	case res.Decision == agent.GuardAllow && !a.needsReview(call, hints):
		a.remember(call, "allowed")
		return res, nil
	}
	verdict, err := a.verdict(ctx, call, res)
	if err != nil {
		return agent.GuardResult{}, err
	}
	review := &llm.ToolReview{Mode: string(config.PermissionModeAuto), Decision: string(verdict.Decision), Stage: verdict.Stage, Reason: verdict.Reason, Classifier: verdict.Classifier}
	switch verdict.Decision {
	case classify.Allow:
		a.count(false)
		a.remember(call, "allowed")
		return agent.GuardResult{Decision: agent.GuardAllow, Revalidate: res.Revalidate, Review: review}, nil
	case classify.Block:
		streak, total := a.count(true)
		a.remember(call, "blocked")
		if !a.headless && (streak >= autoBlockStreak || total >= autoBlockTotal) {
			a.resetStreak()
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
	// No judgement: a person decides, with the gate's own approvals if any.
	a.remember(call, "asked")
	approvals := res.Approvals
	if len(approvals) == 0 {
		approvals = []agent.GuardApproval{{RuleID: ruleAutoUnavailable, Action: approvalAction(call, res)}}
	}
	approvals = slices.Clone(approvals)
	for i := range approvals {
		approvals[i].Reason = strings.TrimSpace(approvals[i].Reason + " Auto mode could not review it: " + verdict.Reason)
	}
	return agent.GuardResult{Decision: agent.GuardAsk, Revalidate: res.Revalidate, Approvals: approvals}, nil
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

func (a *autoMode) verdict(ctx context.Context, call llm.ToolCall, res agent.GuardResult) (classify.Verdict, error) {
	a.mu.Lock()
	key := fmt.Sprintf("%d\x00%s\x00%s", a.intentVer, call.Name, canonicalArguments(call.Arguments))
	if cached, ok := a.cache[key]; ok {
		a.mu.Unlock()
		return cached, nil
	}
	req := classify.Request{
		Intent:      slices.Clone(a.intent),
		History:     slices.Clone(a.history),
		Action:      classifierAction(call, res),
		Environment: gitEnvironment(a.workspace),
	}
	a.mu.Unlock()
	req.Environment.Slots = a.slots
	verdict, err := a.classify(ctx, req)
	if err != nil {
		return classify.Verdict{}, err
	}
	if verdict.Decision != classify.Unavailable {
		a.mu.Lock()
		a.cache[key] = verdict
		a.mu.Unlock()
	}
	return verdict, nil
}

func (a *autoMode) remember(call llm.ToolCall, outcome string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, classify.PastAction{Tool: call.Name, Arguments: call.Arguments, Outcome: outcome})
	if len(a.history) > autoHistoryCap {
		a.history = slices.Clone(a.history[len(a.history)-autoHistoryCap:])
	}
}

func (a *autoMode) count(blocked bool) (streak, total int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if blocked {
		a.streak++
		a.total++
	} else {
		a.streak = 0
	}
	return a.streak, a.total
}

func (a *autoMode) resetStreak() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streak = 0
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

// canonicalArguments makes equivalent JSON spellings share a cache entry.
func canonicalArguments(raw json.RawMessage) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return string(raw)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(canonical)
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
