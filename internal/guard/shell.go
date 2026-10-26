package guard

import (
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// shellWord is one command word. Unresolved words contain expansions whose
// value is unknown statically; $HOME and $TMPDIR prefixes are substituted.
type shellWord struct {
	text       string
	unresolved bool
}

// shellCommand is one simple command after wrapper unwrapping.
type shellCommand struct {
	words []shellWord
	// base resolves relative paths; "" means a preceding cd made it unknown.
	base string
	// extraArgs reports that xargs or find -exec appends unseen arguments.
	extraArgs bool
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
}

// maxShellDepth bounds nested `bash -c` parsing.
const maxShellDepth = 4

// analyzeBash parses command and returns its file actions and command
// findings. Unparseable input falls back to whitespace tokenization.
func analyzeBash(command, workspace string) bashAnalysis {
	a := &bashAnalyzer{command: command, workspace: workspace}
	a.script(command, workspace, 0)
	return bashAnalysis{actions: a.actions, findings: a.findings, commands: a.commands}
}

type bashAnalyzer struct {
	command   string
	workspace string
	actions   []Action
	findings  []finding
	commands  [][]string
}

func (a *bashAnalyzer) script(script, base string, depth int) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		var words []shellWord
		for _, token := range tokenizeBash(script) {
			words = append(words, shellWord{text: token, unresolved: containsExpansion(token)})
		}
		a.simple(shellCommand{words: words, base: base}, depth)
		return
	}
	a.findings = append(a.findings, fetchExecFindings(file)...)
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Stmt:
			for _, r := range n.Redirs {
				a.redirect(r, base)
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			words := make([]shellWord, len(n.Args))
			for i, w := range n.Args {
				words[i] = literalWord(w)
			}
			cmd := shellCommand{words: words, base: base}
			a.simple(cmd, depth)
			base = nextBase(cmd)
		}
		return true
	})
}

// redirect records file effects of redirections. Descriptor duplication and
// here-documents carry no path.
func (a *bashAnalyzer) redirect(r *syntax.Redirect, base string) {
	if r.Word == nil {
		return
	}
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
	w := literalWord(r.Word)
	if w.text != "" {
		a.path(w, op, base, true)
	}
}

// simple classifies one command, unwrapping launchers and nested shells.
func (a *bashAnalyzer) simple(cmd shellCommand, depth int) {
	for {
		if len(cmd.words) > 0 {
			a.commands = append(a.commands, commandWords(cmd))
		}
		inner, privileged, ok := unwrap(cmd)
		if privileged != "" {
			a.findings = append(a.findings, finding{rule: RuleBashPrivileged, desc: privileged, pattern: cmd.display(), words: commandWords(cmd)})
		}
		if !ok {
			break
		}
		cmd = inner
	}
	if len(cmd.words) == 0 {
		return
	}
	if script, ok := shellScript(cmd); ok {
		if depth < maxShellDepth && !script.unresolved {
			a.script(script.text, cmd.base, depth+1)
		}
		return
	}
	if nested, ok := findExec(cmd); ok {
		a.simple(nested, depth)
	}
	a.findings = append(a.findings, classifyCommand(cmd)...)
	for _, arg := range commandPaths(cmd, cmd.base != a.workspace) {
		a.path(arg.word, arg.op, cmd.base, arg.force)
	}
}

// path records one candidate. Non-forced words must look like paths.
func (a *bashAnalyzer) path(w shellWord, op, base string, force bool) {
	if w.text == "" || !force && !maybePathLike(w.text) || neutralDevice(w.text) {
		return
	}
	act := Action{Kind: "file", Op: op, Path: w.text, ToolName: "bash", Unresolved: w.unresolved, Command: a.command}
	if base == "" && !isRootedWord(w.text) {
		act.Unresolved = true
	} else if base != a.workspace && !w.unresolved && !isRootedWord(w.text) {
		act.Path = filepath.Join(base, w.text)
	}
	a.actions = append(a.actions, act)
}

// nextBase follows cd/pushd so later relative paths resolve correctly.
// Subshell scoping is ignored, which only makes later checks stricter.
func nextBase(cmd shellCommand) string {
	if name := cmd.name(); name != "cd" && name != "pushd" {
		return cmd.base
	}
	var args []shellWord
	for _, w := range cmd.words[1:] {
		if !strings.HasPrefix(w.text, "-") {
			args = append(args, w)
		}
	}
	switch {
	case len(args) == 0:
		home, _ := os.UserHomeDir()
		return home
	case len(args) > 1 || args[0].unresolved:
		return ""
	case cmd.base == "" && !isRootedWord(args[0].text):
		return ""
	}
	return resolveAbsolute(args[0].text, cmd.base, "bash")
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

// literalWord renders w with expansions marked. $HOME and $TMPDIR are
// substituted from the environment so common spellings resolve.
func literalWord(w *syntax.Word) shellWord {
	if w == nil {
		return shellWord{}
	}
	var b strings.Builder
	unresolved := false
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					b.WriteString(lit.Value)
				} else if value, ok := knownVariable(inner); ok {
					b.WriteString(value)
				} else {
					b.WriteString("$")
					unresolved = true
				}
			}
		default:
			if value, ok := knownVariable(part); ok {
				b.WriteString(value)
				continue
			}
			b.WriteString("$")
			unresolved = true
		}
	}
	return shellWord{text: b.String(), unresolved: unresolved}
}

// knownVariable substitutes a plain $HOME or $TMPDIR.
func knownVariable(part syntax.WordPart) (string, bool) {
	p, ok := part.(*syntax.ParamExp)
	if !ok || p.Param == nil || p.Exp != nil || p.Slice != nil || p.Repl != nil || p.Index != nil || p.Length || p.Width || p.Excl {
		return "", false
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

// wordToString renders w with each expansion as "$".
func wordToString(w *syntax.Word) string {
	return literalWord(w).text
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
