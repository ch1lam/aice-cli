package guard

import (
	"encoding/json"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Action is the normalized guard input derived from a ToolCall.
type Action struct {
	Kind       string // "file" or "command"
	Path       string
	Command    string
	ToolName   string
	Unresolved bool // true when path contains $VAR / $(...) etc - cannot stat
}

// Decision is the outcome of a guard check.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	DecisionAsk   Decision = "ask"
)

// Result is the outcome of checking one Action against all policies.
type Result struct {
	Decision Decision
	Reason   string
	RuleID   string
	Action   Action
}

// fileTools are tools that carry a single file path argument.
var fileTools = map[string]bool{
	"read": true, "write": true, "edit": true, "grep": true, "find": true, "ls": true,
}

func isKnownTool(name string) bool {
	return fileTools[name] || name == "bash"
}

// extractActions extracts one or more Actions from a ToolCall.
// For file tools it returns one file Action; for bash it extracts candidate
// paths via AST parsing with heuristic fallback.
func extractActions(call llm.ToolCall) []Action {
	if fileTools[call.Name] {
		p := extractFilePath(call.Arguments)
		if strings.TrimSpace(p) == "" {
			return nil
		}
		return []Action{{Kind: "file", Path: p, ToolName: call.Name, Unresolved: containsExpansion(p)}}
	}
	if call.Name == "bash" {
		cmd := extractCommand(call.Arguments)
		if strings.TrimSpace(cmd) == "" {
			return nil
		}
		cands := parsePathCandidates(cmd)
		var out []Action
		for _, c := range cands {
			tok := c.token
			if tok == "" || strings.HasPrefix(tok, "-") {
				continue
			}
			if !c.forcePath && !maybePathLike(tok) {
				continue
			}
			out = append(out, Action{Kind: "file", Path: tok, ToolName: call.Name, Unresolved: c.unresolved, Command: cmd})
		}
		return out
	}
	return nil
}

func extractFilePath(raw json.RawMessage) string {
	var m map[string]any
	if err := jsonutil.DecodeStrict(raw, &m); err != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "file"} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func extractCommand(raw json.RawMessage) string {
	var m map[string]any
	if err := jsonutil.DecodeStrict(raw, &m); err != nil {
		return ""
	}
	if v, ok := m["command"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func containsExpansion(s string) bool {
	return strings.Contains(s, "$") || strings.Contains(s, "`") || strings.Contains(s, "$(")
}

func maybePathLike(s string) bool {
	if s == "" {
		return false
	}
	// Heuristic aligned with pi-guardrails maybePathLike: contains "/" or "."
	// or starts with "~".
	if strings.HasPrefix(s, "~") {
		return true
	}
	if strings.Contains(s, "/") {
		return true
	}
	if strings.Contains(s, ".") {
		// avoid pure numbers like "3.14"
		hasAlpha := false
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '-' {
				hasAlpha = true
				break
			}
		}
		return hasAlpha
	}
	return false
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
	// Strip surrounding quotes from tokens
	for i, t := range tokens {
		tokens[i] = strings.Trim(t, "'\"`")
	}
	return tokens
}
