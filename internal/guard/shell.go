package guard

import (
	"bytes"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// wordToString renders a syntax.Word to its literal string, preserving
// expansions as raw text (e.g. $VAR, $(...)) for conservative handling.
func wordToString(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	var buf bytes.Buffer
	printer := syntax.NewPrinter()
	// Use a minimal file to print just this word. Alternative: manually walk Parts.
	// Simpler: use the printer on the word's parts via DebugPrint fallback.
	// We reconstruct by iterating Parts.
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			buf.WriteString(p.Value)
		case *syntax.SglQuoted:
			buf.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					buf.WriteString(lit.Value)
				} else {
					// Any non-literal inside double quotes is an expansion.
					buf.WriteString("$")
				}
			}
		case *syntax.ParamExp:
			// Conservative: mark as expansion
			buf.WriteString("$")
			if p.Param != nil {
				buf.WriteString(p.Param.Value)
			}
		case *syntax.CmdSubst:
			buf.WriteString("$(...)")
		case *syntax.ArithmExp:
			buf.WriteString("$((...))")
		case *syntax.ProcSubst:
			buf.WriteString("(")
		default:
			// Unknown part type -> treat as expansion placeholder
			_ = printer
			buf.WriteString("$")
		}
	}
	return buf.String()
}

// wordHasExpansion reports whether a word contains any shell expansion that
// cannot be resolved statically. Conservative: any non-literal part counts.
func wordHasExpansion(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, part := range w.Parts {
		switch part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
			continue
		case *syntax.DblQuoted:
			dq := part.(*syntax.DblQuoted)
			for _, inner := range dq.Parts {
				switch inner.(type) {
				case *syntax.Lit:
					continue
				default:
					return true
				}
			}
		default:
			return true
		}
	}
	return false
}

// wordRawString returns the raw literal value for matching, using Lit/SglQuoted
// content directly. For flag detection we want the exact token as typed.
func wordRawString(w *syntax.Word) string {
	return wordToString(w)
}

// parseCallWords splits a shell command into per-command word lists using
// the mvdan.cc/sh parser. Each inner slice is one SimpleCommand's Args.
// Falls back to heuristic tokenization on parse error.
func parseCallWords(command string) [][]string {
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	f, err := p.Parse(strings.NewReader(command), "")
	if err != nil {
		// Fallback: single command heuristic
		return [][]string{tokenizeBash(command)}
	}
	var result [][]string
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			var words []string
			// CallExpr.Args includes the command name plus arguments.
			for _, w := range n.Args {
				words = append(words, wordRawString(w))
			}
			// Only record non-empty commands. This mirrors pi's walkCommands
			// which visits SimpleCommand nodes at any depth.
			if len(words) > 0 {
				result = append(result, words)
			}
		}
		return true
	})
	if len(result) == 0 {
		return [][]string{tokenizeBash(command)}
	}
	return result
}

// parsePathCandidates extracts path-like candidates from a bash command via AST.
// It returns tokens with unresolved flag and handles redirects as forced paths.
func parsePathCandidates(command string) []struct {
	token      string
	unresolved bool
	forcePath  bool
} {
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	f, err := p.Parse(strings.NewReader(command), "")
	if err != nil {
		// Fallback to heuristic
		toks := tokenizeBash(command)
		var out []struct {
			token      string
			unresolved bool
			forcePath  bool
		}
		for _, t := range toks {
			if t == "" || strings.HasPrefix(t, "-") {
				continue
			}
			out = append(out, struct {
				token      string
				unresolved bool
				forcePath  bool
			}{token: t, unresolved: containsExpansion(t)})
		}
		return out
	}
	type cand struct {
		token      string
		unresolved bool
		forcePath  bool
	}
	var cands []cand
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			// Assignment-only commands (e.g. `VAR=val`) parse as a CallExpr
			// with no Args; slicing an empty slice panics.
			if len(n.Args) == 0 {
				return true
			}
			for _, w := range n.Args[1:] {
				// Skip first word (command name) for path extraction; but we handle it as arg
				// Actually we want args after command name. The loop above includes command name,
				// so skip index 0 is already done via Args[1:]. Good.
				s := wordRawString(w)
				if s == "" || strings.HasPrefix(s, "-") {
					continue
				}
				cands = append(cands, cand{token: s, unresolved: wordHasExpansion(w)})
			}
			// Also handle the case where CallExpr is inside assignment? Already covered.
		case *syntax.Stmt:
			// Redirect targets are forced paths (e.g., echo hi > /tmp/file)
			for _, r := range n.Redirs {
				if r.N != nil {
					// r.N is word for redirect target (e.g., "2>file" has N)
					continue
				}
				if r.Word != nil {
					s := wordRawString(r.Word)
					if s != "" {
						cands = append(cands, cand{token: s, unresolved: wordHasExpansion(r.Word), forcePath: true})
					}
				}
			}
		}
		return true
	})
	// If no candidates via AST (e.g., all flags), fall back to heuristic for coverage.
	if len(cands) == 0 {
		toks := tokenizeBash(command)
		for _, t := range toks {
			if t == "" || strings.HasPrefix(t, "-") {
				continue
			}
			cands = append(cands, cand{token: t, unresolved: containsExpansion(t)})
		}
	}
	// Convert to expected return type
	var out []struct {
		token      string
		unresolved bool
		forcePath  bool
	}
	for _, c := range cands {
		out = append(out, struct {
			token      string
			unresolved bool
			forcePath  bool
		}{token: c.token, unresolved: c.unresolved, forcePath: c.forcePath})
	}
	return out
}

// Helpers for structural matchers (mirroring pi's hasShortFlag/hasLongOption/hasArg)

func hasShortFlag(words []string, flag string) bool {
	if len(flag) != 1 {
		return false
	}
	for _, w := range words {
		if w == "-"+flag {
			return true
		}
		if strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.Contains(w, flag) {
			return true
		}
	}
	return false
}

func hasLongOption(words []string, option string) bool {
	target := "--" + option
	for _, w := range words {
		if w == target {
			return true
		}
	}
	return false
}

func hasArg(words []string, prefix string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}
