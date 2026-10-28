package guard

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/hostpath"
)

// Rule decisions and the rule identifiers they produce.
const (
	RuleDecisionAllow = "allow"
	RuleDecisionAsk   = "ask"
	RuleDecisionDeny  = "deny"

	RuleUserAsk  = "rule.ask"
	RuleUserDeny = "rule.deny"
)

// Rules are explicit user rules from settings. Deny wins over ask, and ask
// over allow. Allow rules cover outside-workspace access and command
// findings; they never cover credentials or AICE control files.
type Rules struct {
	Allow []string
	Ask   []string
	Deny  []string
}

// rule is one parsed specifier: bash(words...) or read/write(path pattern).
type rule struct {
	text string
	tool string // "bash", opRead or opWrite
	// words match a command exactly, or as a prefix when prefix is set
	// (written as a trailing "*").
	words  []string
	prefix bool
	// path is an absolute file, glob, or directory when dir is set
	// (written as a trailing "/**").
	path string
	dir  bool
}

type ruleSet struct {
	allow, ask, deny []rule
}

func parseRules(rules Rules, workspace string) (ruleSet, error) {
	var set ruleSet
	for _, group := range []struct {
		texts []string
		into  *[]rule
	}{{rules.Allow, &set.allow}, {rules.Ask, &set.ask}, {rules.Deny, &set.deny}} {
		for _, text := range group.texts {
			parsed, err := parseRule(text, workspace)
			if err != nil {
				return ruleSet{}, err
			}
			*group.into = append(*group.into, parsed)
		}
	}
	return set, nil
}

// parseRule parses bash(go test *), read(~/go/pkg/mod/**) or write(/tmp/x).
// Relative paths resolve against the workspace.
func parseRule(text, workspace string) (rule, error) {
	text = strings.TrimSpace(text)
	tool, rest, ok := strings.Cut(text, "(")
	spec, closed := strings.CutSuffix(rest, ")")
	spec = strings.TrimSpace(spec)
	if !ok || !closed || spec == "" {
		return rule{}, fmt.Errorf("guard: permission rule %q must look like bash(git push *) or read(~/dir/**)", text)
	}
	r := rule{text: text, tool: tool}
	switch tool {
	case "bash":
		r.words = strings.Fields(spec)
		if last := len(r.words) - 1; r.words[last] == "*" {
			r.words, r.prefix = r.words[:last], true
		}
		if len(r.words) == 0 {
			return rule{}, fmt.Errorf("guard: permission rule %q must name a command", text)
		}
	case opRead, opWrite:
		if dir, ok := strings.CutSuffix(filepath.ToSlash(spec), "/**"); ok {
			spec, r.dir = dir, true
			if spec == "" {
				spec = "/"
			}
		}
		// Rules use Bash path syntax: a leading "/" is rooted on Windows too.
		r.path = physicalPath(resolveAbsolute(filepath.ToSlash(spec), workspace, "bash"))
		if r.dir && isGrantTooBroad(r.path) {
			return rule{}, fmt.Errorf("guard: permission rule %q covers the filesystem root or home directory", text)
		}
	default:
		return rule{}, fmt.Errorf("guard: permission rule %q has unknown tool %q (bash, read or write)", text, tool)
	}
	return r, nil
}

// broadExecution reports bash rules that allow arbitrary code, such as
// bash(python *) or bash(npm run *). Auto mode sends those commands to the
// classifier instead of trusting the rule.
func (r rule) broadExecution() bool {
	if r.tool != "bash" || !r.prefix {
		return false
	}
	switch r.words[0] {
	case "python", "python3", "node", "ruby", "perl", "php", "sh", "bash", "zsh", "dash", "ksh", "fish",
		"deno", "bun", "npx", "make", "eval", "exec", "xargs", "env", "sudo":
		return true
	}
	if len(r.words) == 2 && r.words[1] == "run" {
		switch r.words[0] {
		case "npm", "pnpm", "yarn", "go", "cargo", "uv", "poetry":
			return true
		}
	}
	return len(r.words) == 1 && (r.words[0] == "git" || r.words[0] == "docker" || r.words[0] == "kubectl")
}

func (r rule) matchCommand(words []string) bool {
	if r.tool != "bash" || len(words) < len(r.words) || !r.prefix && len(words) != len(r.words) {
		return false
	}
	return slices.Equal(words[:len(r.words)], r.words)
}

// matchPath reports whether r covers an op on abs. coverRead lets a write
// rule cover reads, which holds for allow rules only.
func (r rule) matchPath(abs, op string, coverRead bool) bool {
	if r.tool != op && !(coverRead && r.tool == opWrite && op == opRead) {
		return false
	}
	if r.dir {
		return hostpath.Within(r.path, abs)
	}
	if strings.ContainsAny(r.path, "*?[") {
		matched, _ := filepath.Match(r.path, abs)
		return matched
	}
	return hostpath.Same(r.path, abs)
}

func firstCommandRule(rules []rule, words []string) (rule, bool) {
	for _, r := range rules {
		if r.matchCommand(words) {
			return r, true
		}
	}
	return rule{}, false
}

func firstPathRule(rules []rule, abs, op string, coverRead bool) (rule, bool) {
	for _, r := range rules {
		if r.matchPath(abs, op, coverRead) {
			return r, true
		}
	}
	return rule{}, false
}

// AddRule records a rule for the remainder of the process, typically after
// the application saved the same rule to user settings.
func (g *Guard) AddRule(decision, text string) error {
	if g == nil {
		return nil
	}
	parsed, err := parseRule(text, g.workspace)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch decision {
	case RuleDecisionAllow:
		g.rules.allow = append(g.rules.allow, parsed)
	case RuleDecisionAsk:
		g.rules.ask = append(g.rules.ask, parsed)
	case RuleDecisionDeny:
		g.rules.deny = append(g.rules.deny, parsed)
	default:
		return fmt.Errorf("guard: unknown rule decision %q", decision)
	}
	return nil
}

// SuggestPathRule returns the allow rule for directory dir, or false when it
// would cover the filesystem root or home directory.
func SuggestPathRule(dir string, write bool) (string, bool) {
	dir = filepath.Clean(dir)
	if isGrantTooBroad(dir) {
		return "", false
	}
	tool := opRead
	if write {
		tool = opWrite
	}
	return tool + "(" + filepath.ToSlash(dir) + "/**)", true
}

// persistableRules may be saved as an "always allow" prefix from a prompt.
// Privilege, disk, fetch-exec, permission and delete findings are confirmed
// per command; a user can still write a rule for them in settings.
var persistableRules = []string{RuleBashOutbound, RuleBashLoseWork}

// SuggestCommandRule returns a prefix allow rule covering every finding of
// command, such as bash(git push *), or false when no single persistable
// prefix covers them.
func (g *Guard) SuggestCommandRule(command string) (string, bool) {
	if g == nil {
		return "", false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	prefix := ""
	for _, f := range g.pendingFindings(analyzeBash(command, g.workspace).findings) {
		if !slices.Contains(persistableRules, f.rule) || len(f.words) == 0 {
			return "", false
		}
		next := strings.Join(commandPrefix(f.words), " ")
		if prefix != "" && next != prefix {
			return "", false
		}
		prefix = next
	}
	if prefix == "" {
		return "", false
	}
	return "bash(" + prefix + " *)", true
}

// subcommandTools take a subcommand that defines the operation.
var subcommandTools = map[string]int{
	"git": 1, "npm": 1, "pnpm": 1, "yarn": 1, "bun": 1, "cargo": 1, "docker": 1,
	"podman": 1, "gem": 1, "twine": 1, "go": 1, "kubectl": 1, "gh": 2,
}

// commandPrefix is the command and its subcommands, such as git push or
// gh pr create.
func commandPrefix(words []string) []string {
	n := subcommandTools[words[0]]
	prefix := []string{words[0]}
	for _, w := range words[1:] {
		if len(prefix) > n {
			break
		}
		if strings.HasPrefix(w, "-") {
			break
		}
		prefix = append(prefix, w)
	}
	return prefix
}
