package guard

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ch1lam/aice-cli/internal/hostpath"
	"mvdan.cc/sh/v3/syntax"
)

// shellWord is one command word. Unresolved words contain expansions whose
// value is unknown statically; $HOME and $TMPDIR prefixes are substituted.
type shellWord struct {
	text       string
	unresolved bool
	// substituted reports a command or process substitution in the word.
	substituted bool
}

// shellCommand is one simple command after wrapper unwrapping.
type shellCommand struct {
	words []shellWord
	// dirs are the directories the command may run in.
	dirs []string
	// extraArgs reports that xargs or find -exec appends unseen arguments.
	extraArgs bool
}

// unknownDir stands for a working directory that cannot be determined
// statically, such as after `cd "$X"` or `cd -`; relative paths from it ask.
const unknownDir = ""

// maxDirs bounds the candidate directories of one command; more collapse to
// unknownDir.
const maxDirs = 8

// dirUnion merges sets of candidate directories, sorted and unique.
func dirUnion(sets ...[]string) []string {
	var out []string
	for _, set := range sets {
		out = append(out, set...)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > maxDirs {
		return []string{unknownDir}
	}
	return out
}

func (c shellCommand) name() string {
	if len(c.words) == 0 {
		return ""
	}
	return filepath.Base(c.words[0].text)
}

func (c shellCommand) texts() []string {
	out := make([]string, len(c.words))
	for i, w := range c.words {
		out[i] = w.text
	}
	return out
}

func (c shellCommand) display() string {
	text := strings.Join(c.texts(), " ")
	if len(text) > 60 {
		text = text[:60] + "..."
	}
	return text
}

// bashAnalysis is everything the gate needs from one tool call.
type bashAnalysis struct {
	actions  []Action
	findings []finding
	// commands lists every command as rules see it, launchers included.
	commands [][]string
	// hidden reports effects the command words do not show: code a command
	// may run through an environment variable or git option, or a path
	// computed by a command substitution. Such a line is never read-only.
	hidden bool
}

// maxShellDepth bounds nested `bash -c`, eval and function call parsing.
const maxShellDepth = 4

// analyzeBash parses command and returns its file actions and command
// findings. Unparseable input falls back to whitespace tokenization.
func (g *Guard) analyzeBash(command string) bashAnalysis {
	a := &bashAnalyzer{
		command: command, workspace: g.workspace, start: g.bashDir,
		vars: map[string]string{}, funcs: map[string]*syntax.Stmt{}, bases: map[string]string{},
		cdpath: os.Getenv("CDPATH") != "",
	}
	a.script(command, []string{a.start}, 0)
	// A trap may run at any later point, so its action is checked with the
	// final functions, from an unknown directory and without variables.
	for i := 0; i < len(a.traps); i++ {
		a.vars = map[string]string{}
		a.script(a.traps[i].text, []string{unknownDir}, a.traps[i].depth)
	}
	return bashAnalysis{actions: a.actions, findings: a.findings, commands: a.commands, hidden: a.hidden}
}

// bashAnalyzer follows the shell's working directory as a set of candidate
// directories spelled like bash's $PWD: cd changes it logically, while
// relative paths resolve from its physical directory as the kernel does.
// Subshells, pipelines, substitutions and background jobs restore it; a
// statement that may fail keeps the directory it started in as a candidate.
type bashAnalyzer struct {
	command string
	// workspace resolves relative paths from start, which keep their spelling.
	workspace string
	start     string
	actions   []Action
	findings  []finding
	commands  [][]string
	// vars holds literal shell assignments seen so far, so `X=~/.ssh; cat
	// $X/id` resolves. Subshells restore it; other scoping is ignored.
	vars map[string]string
	// funcs are the functions defined so far; a call checks the body from the
	// caller's directory.
	funcs map[string]*syntax.Stmt
	// bases caches the physical directory of each candidate.
	bases map[string]string
	// cdpath reports that CDPATH may be set, so cd to a bare relative name
	// may land elsewhere.
	cdpath bool
	hidden bool
	// traps are the trap actions set, with the depth to check them at.
	traps []trap
}

type trap struct {
	text  string
	depth int
}

// script checks one script from dirs and returns where it may leave the
// shell: ok after it succeeds, all whatever its status.
func (a *bashAnalyzer) script(script string, dirs []string, depth int) (ok, all []string) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		var words []shellWord
		for _, token := range tokenizeBash(script) {
			words = append(words, shellWord{text: token, unresolved: containsExpansion(token)})
		}
		a.simple(shellCommand{words: words, dirs: dirs}, depth)
		// Bash may still run part of the script, including a cd.
		out := dirUnion(dirs, []string{unknownDir})
		return out, out
	}
	a.findings = append(a.findings, fetchExecFindings(file)...)
	a.cdpath = a.cdpath || mayRedirectCD(file)
	return a.stmts(file.Stmts, dirs, depth)
}

// stmts runs a list in sequence: each statement starts wherever the
// previous one may have left the shell, whether it failed or not.
func (a *bashAnalyzer) stmts(list []*syntax.Stmt, dirs []string, depth int) (ok, all []string) {
	ok, all = dirs, dirs
	for _, s := range list {
		ok, all = a.stmt(s, all, depth)
	}
	return ok, all
}

func (a *bashAnalyzer) stmt(s *syntax.Stmt, dirs []string, depth int) (ok, all []string) {
	for _, r := range s.Redirs {
		a.redirect(r, dirs, depth)
	}
	if s.Background || s.Coprocess {
		a.subshell(func() { a.runCommand(s.Cmd, dirs, depth) })
		return dirs, dirs
	}
	ok, all = a.runCommand(s.Cmd, dirs, depth)
	if s.Negated {
		ok = all
	}
	return ok, all
}

func (a *bashAnalyzer) runCommand(cmd syntax.Command, dirs []string, depth int) (ok, all []string) {
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		for _, assign := range c.Assigns {
			a.substitutions(assign, dirs, depth)
			a.environment(assign)
		}
		if len(c.Args) == 0 {
			for _, assign := range c.Assigns {
				a.assign(assign)
			}
			break
		}
		words := make([]shellWord, len(c.Args))
		for i, w := range c.Args {
			a.substitutions(w, dirs, depth)
			words[i] = a.literalWord(w)
		}
		return a.simple(shellCommand{words: words, dirs: dirs}, depth)
	case *syntax.DeclClause:
		for _, assign := range c.Args {
			a.substitutions(assign, dirs, depth)
			if assign.Naked && assign.Name != nil {
				continue
			}
			a.environment(assign)
			a.assign(assign)
		}
	case *syntax.Block:
		return a.stmts(c.Stmts, dirs, depth)
	case *syntax.Subshell:
		a.subshell(func() { a.stmts(c.Stmts, dirs, depth) })
	case *syntax.TimeClause:
		if c.Stmt != nil {
			return a.stmt(c.Stmt, dirs, depth)
		}
	case *syntax.CoprocClause:
		a.subshell(func() { a.stmt(c.Stmt, dirs, depth) })
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.AndStmt:
			xok, xall := a.stmt(c.X, dirs, depth)
			yok, yall := a.stmt(c.Y, xok, depth)
			return yok, dirUnion(xall, yall)
		case syntax.OrStmt:
			xok, xall := a.stmt(c.X, dirs, depth)
			yok, yall := a.stmt(c.Y, xall, depth)
			return dirUnion(xok, yok), dirUnion(xall, yall)
		}
		// Pipeline parts run in subshells, except the last one under lastpipe.
		a.subshell(func() { a.stmt(c.X, dirs, depth) })
		_, yall := a.stmt(c.Y, dirs, depth)
		out := dirUnion(dirs, yall)
		return out, out
	case *syntax.IfClause:
		out := a.ifClause(c, dirs, depth)
		return out, out
	case *syntax.WhileClause:
		out := a.loop(dirs, func(in []string) []string {
			_, cond := a.stmts(c.Cond, in, depth)
			_, body := a.stmts(c.Do, cond, depth)
			return dirUnion(cond, body)
		})
		return out, out
	case *syntax.ForClause:
		if iter, ok := c.Loop.(*syntax.WordIter); ok {
			// Loop items are expanded like operands, so `for f in ~/.ssh/*` is a read.
			for _, item := range iter.Items {
				a.substitutions(item, dirs, depth)
				a.path(a.literalWord(item), opRead, dirs, false)
			}
			delete(a.vars, iter.Name.Value)
		} else {
			a.substitutions(c.Loop, dirs, depth)
		}
		out := a.loop(dirs, func(in []string) []string {
			_, body := a.stmts(c.Do, in, depth)
			return body
		})
		return out, out
	case *syntax.CaseClause:
		a.substitutions(c.Word, dirs, depth)
		out, prev := dirs, []string(nil)
		for _, item := range c.Items {
			for _, pattern := range item.Patterns {
				a.substitutions(pattern, dirs, depth)
			}
			// ;& and ;;& continue into the next item.
			_, body := a.stmts(item.Stmts, dirUnion(dirs, prev), depth)
			out, prev = dirUnion(out, body), nil
			if item.Op != syntax.Break {
				prev = body
			}
		}
		return out, out
	case *syntax.FuncDecl:
		// Checking the body where it is defined also covers calls the
		// analysis cannot see.
		if c.Name != nil {
			a.funcs[c.Name.Value] = c.Body
		}
		a.subshell(func() { a.stmt(c.Body, dirs, depth) })
	case nil:
	default:
		// Arithmetic, test and let clauses only expand words.
		a.substitutions(c, dirs, depth)
	}
	return dirs, dirs
}

func (a *bashAnalyzer) ifClause(c *syntax.IfClause, dirs []string, depth int) []string {
	_, cond := a.stmts(c.Cond, dirs, depth)
	_, then := a.stmts(c.Then, cond, depth)
	if c.Else == nil {
		return dirUnion(cond, then)
	}
	return dirUnion(then, a.ifClause(c.Else, cond, depth))
}

// loop checks a loop body once from dirs. When the pass may change
// directory, a second pass starts from every directory a later iteration
// may see, unknownDir included.
func (a *bashAnalyzer) loop(dirs []string, pass func([]string) []string) []string {
	out := dirUnion(dirs, pass(dirs))
	if slices.Equal(out, dirUnion(dirs)) {
		return out
	}
	return dirUnion(out, pass(dirUnion(out, []string{unknownDir})))
}

// subshell runs fn and then restores the shell state, as `( )`, pipelines,
// substitutions and background jobs do.
func (a *bashAnalyzer) subshell(fn func()) {
	vars, funcs := maps.Clone(a.vars), maps.Clone(a.funcs)
	fn()
	a.vars, a.funcs = vars, funcs
}

// substitutions checks the commands substituted into node; they run in a
// subshell.
func (a *bashAnalyzer) substitutions(node syntax.Node, dirs []string, depth int) {
	syntax.Walk(node, func(n syntax.Node) bool {
		var stmts []*syntax.Stmt
		switch n := n.(type) {
		case *syntax.CmdSubst:
			stmts = n.Stmts
		case *syntax.ProcSubst:
			stmts = n.Stmts
		default:
			return true
		}
		a.subshell(func() { a.stmts(stmts, dirs, depth) })
		return false
	})
}

// assign records a literal assignment; any other value forgets the name.
func (a *bashAnalyzer) assign(assign *syntax.Assign) {
	if assign.Name == nil || assign.Array != nil || assign.Index != nil {
		return
	}
	value := shellWord{}
	if assign.Value != nil {
		value = a.literalWord(assign.Value)
	}
	if value.unresolved || assign.Append {
		delete(a.vars, assign.Name.Value)
		return
	}
	a.vars[assign.Name.Value] = value.text
}

// environment marks an assignment that may change which code later
// commands run, such as PATH, LD_PRELOAD or GIT_CONFIG_COUNT.
func (a *bashAnalyzer) environment(assign *syntax.Assign) {
	if assign.Name == nil {
		a.hidden = true
		return
	}
	a.hidden = a.hidden || !inertVariable(assign.Name.Value, a.literalWord(assign.Value).text)
}

// redirect records file effects of redirections. Descriptor duplication and
// here-documents carry no path.
func (a *bashAnalyzer) redirect(r *syntax.Redirect, dirs []string, depth int) {
	if r.Hdoc != nil {
		a.substitutions(r.Hdoc, dirs, depth)
	}
	if r.Word == nil {
		return
	}
	a.substitutions(r.Word, dirs, depth)
	op := opWrite
	switch r.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return
	case syntax.RdrIn:
		op = opRead
	case syntax.DplIn, syntax.DplOut:
		if text := wordToString(r.Word); text == "-" || isDigits(text) {
			return
		}
	}
	w := a.literalWord(r.Word)
	if w.text != "" {
		a.path(w, op, dirs, true)
	}
}

// simple classifies one command, unwrapping launchers and nested shells,
// and returns where it leaves the shell.
func (a *bashAnalyzer) simple(cmd shellCommand, depth int) (ok, all []string) {
	in := cmd.dirs
	// Only `command` and `builtin` run their command in this shell; a
	// function is called only by its own name.
	inShell, direct := true, true
	for {
		if len(cmd.words) > 0 {
			a.commands = append(a.commands, commandWords(cmd))
		}
		if script, ok := envSplitString(cmd); ok {
			// env -S splits its value into a command line, like eval.
			a.subshell(func() {
				a.eval(shellCommand{words: append([]shellWord{{text: "eval"}}, script...), dirs: cmd.dirs}, depth)
			})
			return in, in
		}
		inner, privileged, ok := unwrap(cmd)
		if privileged != "" {
			a.findings = append(a.findings, finding{rule: RuleBashPrivileged, desc: privileged, pattern: cmd.display(), words: commandWords(cmd)})
		}
		if !ok {
			break
		}
		if cmd.name() == "env" {
			for _, w := range cmd.words[1 : len(cmd.words)-len(inner.words)] {
				if name, value, ok := strings.Cut(w.text, "="); ok && !strings.HasPrefix(name, "-") {
					a.hidden = a.hidden || !inertVariable(name, value)
				}
			}
		}
		if name := cmd.name(); name != "command" && name != "builtin" {
			inShell = false
		}
		cmd, direct = inner, false
	}
	if len(cmd.words) == 0 {
		return in, in
	}
	if cmd.words[0].unresolved {
		a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "command name from a variable or substitution", pattern: cmd.display()})
	}
	if cmd.name() == "eval" {
		ok, all := a.eval(cmd, depth)
		if !inShell {
			return in, in
		}
		return ok, all
	}
	if cmd.name() == "trap" {
		if action, ok := trapAction(cmd); ok {
			a.hidden = true
			switch {
			case action.unresolved:
				a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "trap of a computed action", pattern: cmd.display()})
			case depth >= maxShellDepth:
				a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "shell scripts nested too deeply to inspect", pattern: cmd.display()})
			default:
				a.traps = append(a.traps, trap{text: action.text, depth: depth + 1})
			}
		}
		return in, in
	}
	if script, ok := shellScript(cmd); ok {
		switch {
		case script.unresolved:
			a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "shell script from a variable or substitution", pattern: cmd.display()})
		case depth >= maxShellDepth:
			a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "shell scripts nested too deeply to inspect", pattern: cmd.display()})
		default:
			a.subshell(func() { a.script(script.text, cmd.dirs, depth+1) })
		}
		return in, in
	}
	if nested, ok := findExec(cmd); ok {
		a.simple(nested, depth)
	}
	a.findings = append(a.findings, classifyCommand(cmd)...)
	a.hidden = a.hidden || gitRunsConfiguredCode(cmd)
	for _, arg := range commandPaths(cmd, len(cmd.dirs) != 1 || cmd.dirs[0] != a.start) {
		a.path(arg.word, arg.op, cmd.dirs, arg.force)
	}
	if !inShell {
		return in, in
	}
	if body := a.funcs[cmd.words[0].text]; body != nil && direct {
		if depth >= maxShellDepth {
			a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "shell functions nested too deeply to inspect", pattern: cmd.display()})
			out := dirUnion(in, []string{unknownDir})
			return out, out
		}
		return a.stmt(body, in, depth+1)
	}
	return a.changeDir(cmd)
}

// trapAction returns the command a trap sets. Listing, a lone condition,
// "-" (reset) and "" (ignore) set none.
func trapAction(cmd shellCommand) (shellWord, bool) {
	args := cmd.words[1:]
	if len(args) > 0 && args[0].text == "--" {
		args = args[1:]
	} else if len(args) > 0 && strings.HasPrefix(args[0].text, "-") && args[0].text != "-" {
		return shellWord{}, false
	}
	if len(args) < 2 || args[0].text == "-" || args[0].text == "" && !args[0].unresolved {
		return shellWord{}, false
	}
	return args[0], true
}

// eval checks a literal script like `bash -c`, in this shell; anything else
// asks.
func (a *bashAnalyzer) eval(cmd shellCommand, depth int) (ok, all []string) {
	unknown := dirUnion(cmd.dirs, []string{unknownDir})
	parts := make([]string, 0, len(cmd.words)-1)
	for _, w := range cmd.words[1:] {
		if w.unresolved {
			a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "eval of a computed script", pattern: cmd.display()})
			return unknown, unknown
		}
		parts = append(parts, w.text)
	}
	if depth >= maxShellDepth {
		a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "shell scripts nested too deeply to inspect", pattern: cmd.display()})
		return unknown, unknown
	}
	return a.script(strings.Join(parts, " "), cmd.dirs, depth+1)
}

// changeDir returns where cd or pushd leaves the shell: ok when it
// succeeds, all also when it fails and stays put. popd, source and . may
// move to a directory the analysis cannot know.
func (a *bashAnalyzer) changeDir(cmd shellCommand) (ok, all []string) {
	in := cmd.dirs
	targets := []string{unknownDir}
	switch cmd.name() {
	case "cd", "pushd":
		if target, known := cdTarget(cmd); known {
			targets = a.cdTargets(target, in)
		}
	case "popd", "source", ".":
		out := dirUnion(in, targets)
		return out, out
	default:
		return in, in
	}
	return targets, dirUnion(in, targets)
}

// cdTarget returns the operand of cd or pushd, "~" for a bare cd, or false
// when it cannot be determined statically: an expansion, `cd -`, a
// directory-stack operation or an unknown option.
func cdTarget(cmd shellCommand) (string, bool) {
	words := cmd.words[1:]
	for len(words) > 0 && len(words[0].text) > 1 && strings.HasPrefix(words[0].text, "-") {
		flag := words[0].text
		words = words[1:]
		if flag == "--" {
			break
		}
		// -L, -P and -e only choose how the target resolves.
		if strings.Trim(flag[1:], "LPe") != "" {
			return "", false
		}
	}
	switch {
	case len(words) == 0:
		return "~", cmd.name() == "cd"
	case len(words) > 1 || words[0].unresolved || words[0].text == "-":
		return "", false
	}
	return words[0].text, true
}

// cdTargets returns where cd to text lands from each of dirs: bash's
// logical path and, when text has "..", the physical one `cd -P` reaches.
// A target that is not an existing directory, ~user and a bare name
// CDPATH may redirect are unknownDir.
func (a *bashAnalyzer) cdTargets(text string, dirs []string) []string {
	rooted := isRootedWord(text)
	if strings.HasPrefix(text, "~") && hostpath.ExpandTilde(text) == text {
		return []string{unknownDir}
	}
	var out []string
	if a.cdpath && !rooted && text != "." && text != ".." && !strings.HasPrefix(text, "./") && !strings.HasPrefix(text, "../") {
		out = append(out, unknownDir)
	}
	dotdot := slices.Contains(strings.Split(filepath.ToSlash(text), "/"), "..")
	for _, dir := range dirs {
		if dir == unknownDir && !rooted {
			out = append(out, unknownDir)
			continue
		}
		logical := resolveAbsolute(text, dir, "bash")
		if info, err := os.Stat(logical); err != nil || !info.IsDir() {
			out = append(out, unknownDir)
			continue
		}
		out = append(out, logical)
		if dotdot {
			from := hostpath.ExpandTilde(text)
			if !rooted {
				from = a.base(dir) + string(filepath.Separator) + from
			}
			if physical, err := filepath.EvalSymlinks(from); err == nil {
				out = append(out, physical)
			}
		}
	}
	return dirUnion(out)
}

// base caches resolveBase for one candidate directory.
func (a *bashAnalyzer) base(dir string) string {
	base, ok := a.bases[dir]
	if !ok {
		base = resolveBase(dir, a.start, a.workspace)
		a.bases[dir] = base
	}
	return base
}

// resolveBase is the directory relative paths from dir resolve against:
// the workspace for the start directory, otherwise dir with symlinks
// resolved, since the kernel resolves ".." from the physical directory.
func resolveBase(dir, start, workspace string) string {
	switch dir {
	case unknownDir:
		return ""
	case start:
		return workspace
	}
	return physicalPath(dir)
}

// path records one candidate from each directory it may be relative to.
// Non-forced words must look like paths.
func (a *bashAnalyzer) path(w shellWord, op string, dirs []string, force bool) {
	a.hidden = a.hidden || w.substituted
	if w.text == "" || !force && !maybePathLike(w.text) || neutralDevice(w.text) {
		return
	}
	act := Action{Kind: "file", Op: op, Path: w.text, ToolName: "bash", Unresolved: w.unresolved, Command: a.command}
	if w.unresolved || isRootedWord(w.text) {
		a.actions = append(a.actions, act)
		return
	}
	for _, dir := range dirs {
		act := act
		switch dir {
		case unknownDir:
			act.Unresolved = true
			a.findings = append(a.findings, finding{rule: RuleBashDynamic, desc: "relative path after a directory change that cannot be determined", pattern: w.text})
		case a.start:
		default:
			act.Path = filepath.Join(a.base(dir), w.text)
		}
		a.actions = append(a.actions, act)
	}
}

// mayRedirectCD reports whether file may set CDPATH, which redirects cd to
// a bare relative name: a word or assignment naming it, or a variable whose
// name is computed at run time.
func mayRedirectCD(file *syntax.File) bool {
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Word:
			found = found || strings.Contains(wordToString(n), "CDPATH")
		case *syntax.Assign:
			found = found || n.Name != nil && n.Name.Value == "CDPATH" || n.Name == nil && n.Value != nil && expandWord(n.Value, nil).unresolved
		case *syntax.CallExpr:
			if len(n.Args) < 2 {
				break
			}
			switch wordToString(n.Args[0]) {
			case "printf":
				found = found || strings.HasPrefix(wordToString(n.Args[1]), "-v")
			case "read", "mapfile", "readarray", "getopts":
				found = found || slices.ContainsFunc(n.Args[1:], func(w *syntax.Word) bool { return expandWord(w, nil).unresolved })
			}
		}
		return !found
	})
	return found
}

// isRootedWord reports words that name a location independent of the current
// directory: absolute, home-relative, drive or UNC paths.
func isRootedWord(text string) bool {
	return filepath.IsAbs(filepath.FromSlash(text)) || strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~") ||
		strings.HasPrefix(text, `\\`) || len(text) >= 2 && isDriveLetter(text[0]) && text[1] == ':'
}

// neutralDevice reports pseudo-devices that expose no file contents.
func neutralDevice(text string) bool {
	switch text {
	case "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/stdin", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return true
	}
	if rest, ok := strings.CutPrefix(text, "/dev/fd/"); ok {
		return isDigits(rest)
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// literalWord renders w as the shell would, with unknown expansions marked
// unresolved. Recorded literal assignments, $HOME and $TMPDIR are
// substituted; unquoted backslash escapes are removed, so `\rm` is rm.
func (a *bashAnalyzer) literalWord(w *syntax.Word) shellWord {
	return expandWord(w, a.vars)
}

func expandWord(w *syntax.Word, vars map[string]string) shellWord {
	if w == nil {
		return shellWord{}
	}
	var b strings.Builder
	unresolved := false
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(unescape(p.Value))
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					b.WriteString(lit.Value)
				} else if value, ok := knownVariable(inner, vars); ok {
					b.WriteString(value)
				} else {
					b.WriteString("$")
					unresolved = true
				}
			}
		default:
			if value, ok := knownVariable(part, vars); ok {
				b.WriteString(value)
				continue
			}
			b.WriteString("$")
			unresolved = true
		}
	}
	substituted := false
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			substituted = true
		}
		return !substituted
	})
	return shellWord{text: b.String(), unresolved: unresolved, substituted: substituted}
}

// unescape removes the backslashes of an unquoted literal.
func unescape(lit string) string {
	if !strings.Contains(lit, `\`) {
		return lit
	}
	var b strings.Builder
	for i := 0; i < len(lit); i++ {
		if lit[i] == '\\' && i+1 < len(lit) {
			i++
			if lit[i] == '\n' {
				continue
			}
		} else if lit[i] == '\\' {
			continue
		}
		b.WriteByte(lit[i])
	}
	return b.String()
}

// knownVariable substitutes a plain recorded variable, $HOME or $TMPDIR.
func knownVariable(part syntax.WordPart, vars map[string]string) (string, bool) {
	p, ok := part.(*syntax.ParamExp)
	if !ok || p.Param == nil || p.Exp != nil || p.Slice != nil || p.Repl != nil || p.Index != nil || p.Length || p.Width || p.Excl {
		return "", false
	}
	if value, ok := vars[p.Param.Value]; ok {
		return value, true
	}
	switch p.Param.Value {
	case "HOME":
		home, err := os.UserHomeDir()
		return home, err == nil && home != ""
	case "TMPDIR":
		dir := os.Getenv("TMPDIR")
		return dir, dir != ""
	}
	return "", false
}

// wordToString renders w with each unknown expansion as "$".
func wordToString(w *syntax.Word) string {
	return expandWord(w, nil).text
}

// fetchExecFindings detects downloaded content piped or substituted into a
// shell or interpreter, such as `curl URL | sh` or `bash <(curl URL)`.
func fetchExecFindings(file *syntax.File) []finding {
	var out []finding
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && containsFetch(n.X) {
				if name := firstCommand(n.Y); interpreters[name] {
					out = append(out, finding{rule: RuleBashFetchExec, desc: "downloaded content executed by " + name, pattern: name})
				}
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			name := filepath.Base(wordToString(n.Args[0]))
			if !interpreters[name] && name != "eval" && name != "source" && name != "." {
				return true
			}
			for _, arg := range n.Args[1:] {
				if containsFetch(arg) {
					out = append(out, finding{rule: RuleBashFetchExec, desc: "downloaded content executed by " + name, pattern: name})
					break
				}
			}
		}
		return true
	})
	return out
}

func containsFetch(node syntax.Node) bool {
	found := false
	syntax.Walk(node, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			switch filepath.Base(wordToString(call.Args[0])) {
			case "curl", "wget":
				found = true
			}
		}
		return !found
	})
	return found
}

func firstCommand(node syntax.Node) string {
	name := ""
	syntax.Walk(node, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 0 && name == "" {
			name = filepath.Base(wordToString(call.Args[0]))
		}
		return name == ""
	})
	return name
}

// tokenizeBash does minimal shell tokenization: split on whitespace and
// shell operators, honor single/double quotes and backticks.
func tokenizeBash(cmd string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble, inBacktick := false, false, false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range cmd {
		switch r {
		case '\'':
			if !inDouble && !inBacktick {
				inSingle = !inSingle
				cur.WriteRune(r)
				continue
			}
		case '"':
			if !inSingle && !inBacktick {
				inDouble = !inDouble
				cur.WriteRune(r)
				continue
			}
		case '`':
			if !inSingle && !inDouble {
				inBacktick = !inBacktick
				cur.WriteRune(r)
				continue
			}
		}
		if !inSingle && !inDouble && !inBacktick {
			if r == ' ' || r == '\t' || r == '\n' || r == '|' || r == '&' || r == ';' || r == '<' || r == '>' || r == '(' || r == ')' {
				flush()
				continue
			}
		}
		cur.WriteRune(r)
	}
	flush()
	for i, t := range tokens {
		tokens[i] = strings.Trim(t, "'\"`")
	}
	return tokens
}

func containsExpansion(s string) bool {
	return strings.Contains(s, "$") || strings.Contains(s, "`")
}

func hasShortFlag(words []string, flag string) bool {
	if len(flag) != 1 {
		return false
	}
	for _, w := range words {
		if strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.Contains(w[1:], flag) {
			return true
		}
	}
	return false
}

func hasLongOption(words []string, option string) bool {
	target := "--" + option
	for _, w := range words {
		if w == target || strings.HasPrefix(w, target+"=") {
			return true
		}
	}
	return false
}
