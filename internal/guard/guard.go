package guard

import (
	"context"
	"fmt"
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
	mu              sync.RWMutex
	workspace       string
	readOnlyRoots   []string
	credentialFiles []string
	controlFiles    []string
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
	return &Guard{
		workspace:           workspace,
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
	cleaned := filepath.Clean(absPath)
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
	actions, findings := extractActions(call, g.workspace)
	var approvals []Approval
	if call.Name == "bash" {
		if approval, ok := g.commandApproval(extractCommand(call.Arguments), findings); ok {
			approvals = append(approvals, approval)
		}
	}
	seen := make(map[string]bool)
	for _, act := range actions {
		abs := resolveAbsolute(act.Path, g.workspace, act.ToolName)
		if secretFileTools[call.Name] {
			if !act.Unresolved && g.isCredentialPath(abs) {
				return Result{Decision: DecisionDeny, Reason: secretDenial(normalizeTarget(act.Path, g.workspace)), RuleID: credentialsRuleID, Action: act}, nil
			}
			if reason, denied := checkSecretFile(ctx, act, g.workspace); denied {
				return Result{Decision: DecisionDeny, Reason: reason, RuleID: secretFilesRuleID, Action: act}, nil
			}
		}
		write := act.Op == opWrite
		key := act.Op + "\x00" + abs
		if seen[key] {
			continue
		}
		seen[key] = true
		if write && g.isControlPath(abs) {
			approvals = append(approvals, Approval{
				Reason: fmt.Sprintf("Changing %s requires confirmation (AICE configuration).", resolveForDisplay(abs, g.workspace)),
				RuleID: RuleControlFile, Action: act,
			})
			continue
		}
		if g.workspace != "" && hostpath.Within(g.workspace, abs) ||
			!write && inReadOnlyRoot(abs, g.readOnlyRoots) ||
			g.pathGranted(abs, write) {
			continue
		}
		rule, verb := RuleOutsideRead, "Reading"
		if write {
			rule, verb = RuleOutsideWrite, "Writing"
		}
		approvals = append(approvals, Approval{
			Reason: fmt.Sprintf("%s %s requires confirmation (outside working directory).", verb, resolveForDisplay(abs, g.workspace)),
			RuleID: rule, Action: act,
		})
	}
	if len(approvals) > 0 {
		return Result{Decision: DecisionAsk, Approvals: approvals}, nil
	}
	return Result{Decision: DecisionAllow}, nil
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

// commandApproval combines the findings of one bash command into a single
// approval named after the most severe rule. An exact Session grant covers it.
func (g *Guard) commandApproval(cmd string, findings []finding) (Approval, bool) {
	if cmd == "" || g.sessionCommands[cmd] {
		return Approval{}, false
	}
	var kept []finding
	for _, f := range findings {
		if f.scoped && !g.riskyDelete(f) {
			continue
		}
		if !slices.ContainsFunc(kept, func(k finding) bool { return k.rule == f.rule && k.desc == f.desc }) {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		return Approval{}, false
	}
	slices.SortStableFunc(kept, func(a, b finding) int {
		return slices.Index(bashRuleOrder, a.rule) - slices.Index(bashRuleOrder, b.rule)
	})
	descs := make([]string, len(kept))
	for i, f := range kept {
		descs[i] = f.desc
	}
	return Approval{
		Reason:  "Command requires confirmation (" + strings.Join(descs, "; ") + "): " + kept[0].pattern,
		RuleID:  kept[0].rule,
		Action:  Action{Kind: "command", Command: cmd, ToolName: "bash"},
		Pattern: kept[0].pattern,
	}, true
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

func cleanPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}
