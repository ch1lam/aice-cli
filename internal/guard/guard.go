package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/hostpath"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func resolveAbsolute(p, workspace, toolName string) string {
	expanded := hostpath.ExpandTilde(p)
	if filepath.IsAbs(expanded) || isBashRootedPath(expanded, toolName) {
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
	return toolName == "bash" && hostpath.IsBashRooted(path)
}

// Guard is the built-in execution gate. Its policies are fixed; bound
// service state and Session grants are protected by mu.
type Guard struct {
	mu        sync.RWMutex
	workspace string
	// physicalWorkspace is workspace with symlinks resolved; boundary
	// checks compare physical paths.
	physicalWorkspace string
	readOnlyRoots     []string
	credentialFiles   []string
	controlFiles      []string
	// rules are explicit settings rules; AddRule appends under mu.
	rules ruleSet
	// sessionAllowedTools tracks unknown tool names allowed for this Session.
	sessionAllowedTools map[string]bool
	// sessionCommands are bash commands granted by exact equality.
	sessionCommands map[string]bool
	// sessionPaths are outside-workspace grants.
	sessionPaths []pathGrant
	// searchTarget is the bound search service fingerprint set by the
	// application per run. An empty value means web_search is unbound.
	searchTarget string
	mcpServices  map[mcpServiceKey]*mcpServiceState
	mcpEpoch     uint64
	mcpSession   uint64
}

// pathGrant covers one file, or a directory and its descendants. A write
// grant also covers reads.
type pathGrant struct {
	path  string
	dir   bool
	write bool
}

// New constructs a Guard for the given workspace and configuration.
// workspace may be empty (uses raw paths without relative conversion).
func New(workspace string, cfg Config) (*Guard, error) {
	rules, err := parseRules(cfg.Rules, workspace)
	if err != nil {
		return nil, err
	}
	return &Guard{
		rules:               rules,
		workspace:           workspace,
		physicalWorkspace:   physicalWorkspace(workspace),
		readOnlyRoots:       resolveReadOnlyRoots(cfg.ReadOnlyRoots, workspace),
		credentialFiles:     cleanPaths(cfg.CredentialFiles),
		controlFiles:        cleanPaths(cfg.ControlFiles),
		sessionAllowedTools: map[string]bool{},
		sessionCommands:     map[string]bool{},
	}, nil
}

// AllowPathSession records an outside-workspace grant for the remainder of
// this Session. isDir covers the directory and its descendants; write also
// covers reads. Grants of the filesystem root or the user's home directory
// are ignored so a Session grant cannot authorize those too-broad scopes.
func (g *Guard) AllowPathSession(absPath string, isDir, write bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cleaned := physicalPath(filepath.Clean(absPath))
	if isGrantTooBroad(cleaned) {
		return
	}
	g.sessionPaths = append(g.sessionPaths, pathGrant{path: cleaned, dir: isDir, write: write})
}

// AllowCommandSession records that a bash command needing confirmation is
// allowed for the remainder of this Session. Only an identical raw command
// bypasses command findings; file and path checks still apply.
func (g *Guard) AllowCommandSession(command string) {
	if g == nil || command == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionCommands[command] = true
}

// AllowToolSession records that an unknown tool name is allowed for the
// remainder of this session.
func (g *Guard) AllowToolSession(name string) {
	if g == nil || name == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionAllowedTools[name] = true
}

// ResetSessionGrants removes only in-memory authorizations for the previous
// Session. Configuration and workspace boundaries remain unchanged. Callers
// must stop active work before resetting the Session. Checks and grants are concurrent-safe.
func (g *Guard) ResetSessionGrants() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(g.sessionAllowedTools)
	clear(g.sessionCommands)
	g.sessionPaths = nil
	g.mcpSession++
	for _, service := range g.mcpServices {
		clear(service.granted)
	}
}

// ResolveAbsolute exposes resolveAbsolute for callers that need to map a
// user-granted path to the absolute form stored in session grants.
func (g *Guard) ResolveAbsolute(p, toolName string) string {
	return resolveAbsolute(p, g.workspace, toolName)
}

// Workspace returns the workspace this guard is scoped to.
func (g *Guard) Workspace() string { return g.workspace }

// Check evaluates one ToolCall against credential and secret-file denials,
// bash command findings, AICE control files and outside-workspace access. It
// returns DecisionDeny for hard blocks and DecisionAsk when user confirmation
// is required. Non-interactive callers treat Ask as Deny (fail-closed); the
// interactive TUI confirms every approval in an Ask. Pending asks never
// short-circuit checks that could deny.
func (g *Guard) Check(ctx context.Context, call llm.ToolCall) (Result, error) {
	if g == nil {
		return Result{Decision: DecisionAllow}, nil
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("guard: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	// Known tools with no extractable path/command still allow. Completely
	// unknown names cannot be mapped to actions and must not be silent-allow,
	// unless the user granted this tool for the remainder of the session.
	if !isKnownTool(call.Name) {
		if g.sessionAllowedTools[call.Name] {
			return Result{Decision: DecisionAllow}, nil
		}
		return Result{Decision: DecisionAsk, Approvals: []Approval{{
			Reason: fmt.Sprintf("tool %q is not recognized by the execution gate", call.Name),
			RuleID: "unknownTool", Action: Action{ToolName: call.Name},
		}}}, nil
	}
	if isNetworkTool(call.Name) {
		return g.checkNetwork(call), nil
	}
	analysis := extractActions(call, g.workspace)
	var approvals []Approval
	if call.Name == "bash" {
		cmd := extractCommand(call.Arguments)
		for _, words := range analysis.commands {
			if r, ok := firstCommandRule(g.rules.deny, words); ok {
				return Result{Decision: DecisionDeny, Reason: "Command " + strings.Join(words, " ") + " is denied by permission rule " + r.text + ".", RuleID: RuleUserDeny, Action: Action{Kind: "command", Command: cmd, ToolName: "bash"}}, nil
			}
		}
		if approval, ok := g.commandApproval(cmd, analysis); ok {
			approvals = append(approvals, approval)
		}
	}
	seen := make(map[string]bool)
	for _, act := range analysis.actions {
		abs := resolveAbsolute(act.Path, g.workspace, act.ToolName)
		phys := physicalPath(abs)
		if denied, ok := g.denyPath(ctx, call.Name, act, abs, phys); ok {
			return denied, nil
		}
		key := act.Op + "\x00" + phys
		if seen[key] {
			continue
		}
		seen[key] = true
		if approval, ok := g.pathApproval(act, abs, phys); ok {
			approvals = append(approvals, approval)
		}
	}
	if len(approvals) > 0 {
		return Result{Decision: DecisionAsk, Approvals: approvals}, nil
	}
	result := Result{Decision: DecisionAllow}
	if call.Name == "bash" {
		result.ReadOnly, result.RuleCovered = g.bashHints(extractCommand(call.Arguments), analysis)
	}
	return result, nil
}

// bashHints classifies an allowed bash command for auto mode routing.
func (g *Guard) bashHints(cmd string, analysis bashAnalysis) (readOnly, covered bool) {
	readOnly = len(analysis.findings) == 0 && !slices.ContainsFunc(analysis.actions, func(a Action) bool { return a.Op == opWrite })
	// Writes into .git (hooks, config) run code later, so they always need review.
	gitDir := filepath.Join(g.physicalWorkspace, ".git")
	if slices.ContainsFunc(analysis.actions, func(a Action) bool {
		return a.Op == opWrite && hostpath.Within(gitDir, physicalPath(resolveAbsolute(a.Path, g.workspace, a.ToolName)))
	}) {
		return false, false
	}
	covered = g.sessionCommands[cmd]
	allCovered := true
	for _, words := range analysis.commands {
		if isLauncher(words[0]) {
			continue
		}
		read := readOnlyCommand(words)
		readOnly = readOnly && read
		if !read && !slices.ContainsFunc(g.rules.allow, func(r rule) bool { return !r.broadExecution() && r.matchCommand(words) }) {
			allCovered = false
		}
	}
	return readOnly, covered || allCovered && len(analysis.commands) > 0
}

// denyPath applies hard denials to the spelled and the physical path.
func (g *Guard) denyPath(ctx context.Context, toolName string, act Action, abs, phys string) (Result, bool) {
	if secretFileTools[toolName] {
		if !act.Unresolved && (g.isCredentialPath(abs) || g.isCredentialPath(phys)) {
			return Result{Decision: DecisionDeny, Reason: secretDenial(normalizeTarget(act.Path, g.workspace)), RuleID: credentialsRuleID, Action: act}, true
		}
		if reason, denied := checkSecretFile(ctx, act, g.workspace); denied {
			return Result{Decision: DecisionDeny, Reason: reason, RuleID: secretFilesRuleID, Action: act}, true
		}
	}
	for _, p := range []string{abs, phys} {
		if r, ok := firstPathRule(g.rules.deny, p, act.Op, false); ok {
			return Result{Decision: DecisionDeny, Reason: "Access to " + resolveForDisplay(p, g.workspace) + " is denied by permission rule " + r.text + ".", RuleID: RuleUserDeny, Action: act}, true
		}
	}
	return Result{}, false
}

// pathApproval returns the confirmation an action needs. Control files and
// ask rules match either spelling; whether access stays inside the
// workspace, a read-only root, a grant or an allow rule is decided on the
// physical path, so a symlink cannot carry access outside.
func (g *Guard) pathApproval(act Action, abs, phys string) (Approval, bool) {
	write := act.Op == opWrite
	display := resolveForDisplay(abs, g.workspace)
	// A symlink inside the workspace that leads outside it is shown and
	// granted by its target.
	escapes := g.workspace != "" && hostpath.Within(g.workspace, abs) && !hostpath.Within(g.physicalWorkspace, phys)
	if escapes {
		display += " (resolves to " + hostpath.HomeDisplay(phys) + ")"
	}
	if write && (g.isControlPath(abs) || g.isControlPath(phys)) {
		return Approval{
			Reason: fmt.Sprintf("Changing %s requires confirmation (AICE configuration).", display),
			RuleID: RuleControlFile, Action: act,
		}, true
	}
	for _, p := range []string{abs, phys} {
		if r, ok := firstPathRule(g.rules.ask, p, act.Op, false); ok {
			return Approval{
				Reason: fmt.Sprintf("Access to %s requires confirmation (permission rule %s).", display, r.text),
				RuleID: RuleUserAsk, Action: act,
			}, true
		}
	}
	if g.physicalWorkspace != "" && hostpath.Within(g.physicalWorkspace, phys) ||
		!write && inReadOnlyRoot(phys, g.readOnlyRoots) ||
		g.pathGranted(phys, write) {
		return Approval{}, false
	}
	if _, ok := firstPathRule(g.rules.allow, phys, act.Op, true); ok {
		return Approval{}, false
	}
	rule, verb := RuleOutsideRead, "Reading"
	if write {
		rule, verb = RuleOutsideWrite, "Writing"
	}
	if escapes {
		act.Path = phys
	}
	return Approval{
		Reason: fmt.Sprintf("%s %s requires confirmation (outside working directory).", verb, display),
		RuleID: rule, Action: act,
	}, true
}

// physicalPath resolves symlinks in the longest existing prefix of abs and
// keeps the missing remainder, so new files resolve through their parents.
// A dangling symlink on the way resolves by its target, so a link to a
// missing credential directory is still checked as that directory.
func physicalPath(abs string) string {
	return physicalPathDepth(abs, 0)
}

// maxSymlinkHops bounds dangling-link resolution against link cycles.
const maxSymlinkHops = 40

func physicalPathDepth(abs string, hops int) string {
	rest := ""
	for p := abs; ; {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		if target, err := os.Readlink(p); err == nil && hops < maxSymlinkHops {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(p), target)
			}
			return physicalPathDepth(filepath.Join(target, rest), hops+1)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return abs
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// Path rule identifiers.
const (
	RuleOutsideRead  = "path.outside.read"
	RuleOutsideWrite = "path.outside.write"
	RuleControlFile  = "controlFile.write"
)

func (g *Guard) pathGranted(abs string, write bool) bool {
	for _, grant := range g.sessionPaths {
		if write && !grant.write {
			continue
		}
		if grant.dir && hostpath.Within(grant.path, abs) || hostpath.Same(grant.path, abs) {
			return true
		}
	}
	return false
}

// commandApproval combines the findings and ask-rule matches of one bash
// command into a single approval named after the most severe rule. An exact
// Session grant covers it.
func (g *Guard) commandApproval(cmd string, analysis bashAnalysis) (Approval, bool) {
	if cmd == "" {
		return Approval{}, false
	}
	kept := g.pendingFindings(analysis.findings)
	if g.sessionCommands[cmd] {
		// An exact grant never covers changes to AICE's own configuration.
		kept = slices.DeleteFunc(kept, func(f finding) bool { return f.rule != RuleControlFile })
	} else {
		for _, words := range analysis.commands {
			if r, ok := firstCommandRule(g.rules.ask, words); ok {
				kept = append(kept, finding{rule: RuleUserAsk, desc: "permission rule " + r.text, pattern: strings.Join(words, " ")})
			}
		}
	}
	if len(kept) == 0 {
		return Approval{}, false
	}
	slices.SortStableFunc(kept, func(a, b finding) int {
		return severity(a.rule) - severity(b.rule)
	})
	descs := make([]string, 0, len(kept))
	for _, f := range kept {
		if !slices.Contains(descs, f.desc) {
			descs = append(descs, f.desc)
		}
	}
	return Approval{
		Reason:  "Command requires confirmation (" + strings.Join(descs, "; ") + "): " + kept[0].pattern,
		RuleID:  kept[0].rule,
		Action:  Action{Kind: "command", Command: cmd, ToolName: "bash"},
		Pattern: kept[0].pattern,
	}, true
}

// severity orders command rules: AICE configuration first, user ask rules
// last.
func severity(ruleID string) int {
	if ruleID == RuleControlFile {
		return -1
	}
	if i := slices.Index(bashRuleOrder, ruleID); i >= 0 {
		return i
	}
	return len(bashRuleOrder)
}

// pendingFindings drops safe deletes, findings covered by allow rules and
// duplicates. The caller holds g.mu.
func (g *Guard) pendingFindings(findings []finding) []finding {
	var kept []finding
	for _, f := range findings {
		if f.scoped && !g.riskyDelete(f) {
			continue
		}
		if _, ok := firstCommandRule(g.rules.allow, f.words); ok && len(f.words) > 0 && f.rule != RuleControlFile {
			continue
		}
		if !slices.ContainsFunc(kept, func(k finding) bool { return k.rule == f.rule && k.desc == f.desc }) {
			kept = append(kept, f)
		}
	}
	return kept
}

// riskyDelete reports whether a recursive delete may reach beyond concrete
// workspace paths: unknown targets, globs at the workspace root, the
// workspace itself, its .git directory, or anything outside it.
func (g *Guard) riskyDelete(f finding) bool {
	if g.workspace == "" || f.extraArgs {
		return true
	}
	for _, target := range f.deleteTargets {
		text := target.text
		if target.unresolved || f.base == "" && !isRootedWord(text) || text == "" {
			return true
		}
		dir, last := filepath.Split(filepath.FromSlash(text))
		if strings.ContainsAny(dir, "*?[") {
			return true
		}
		abs := resolveAbsolute(text, f.base, "bash")
		if strings.ContainsAny(last, "*?[") {
			abs = filepath.Dir(abs)
			if hostpath.Same(abs, g.workspace) {
				return true
			}
		}
		if !hostpath.Within(g.workspace, abs) || hostpath.Within(filepath.Join(g.workspace, ".git"), abs) {
			return true
		}
		if hostpath.Same(abs, g.workspace) && !f.filtered {
			return true
		}
	}
	return false
}

func physicalWorkspace(workspace string) string {
	if workspace == "" {
		return ""
	}
	return physicalPath(filepath.Clean(workspace))
}

func cleanPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, physicalPath(filepath.Clean(p)))
		}
	}
	return out
}
