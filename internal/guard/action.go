package guard

import (
	"encoding/json"
	"strings"

	"github.com/ch1lam/aice-cli/internal/jsonutil"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Action is the normalized guard input derived from a ToolCall.
type Action struct {
	Kind string // "file", "command", "network" or "mcp"
	// Op is "read" or "write" for file actions.
	Op         string
	Path       string
	Command    string
	ToolName   string
	Unresolved bool // true when path contains $VAR / $(...) etc - cannot stat
	// Target is the network identity of a "network" action: the bound search
	// service fingerprint for web_search or the fetch origin for web_fetch.
	// For MCP it is the source-qualified service label for approval display.
	Target string
}

// Decision is the outcome of a guard check.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	DecisionAsk   Decision = "ask"
)

// Result is the final decision for a tool call. Denial diagnostics are separate
// from the complete list of independent approvals required by an ask.
type Result struct {
	Decision  Decision
	Reason    string
	RuleID    string
	Action    Action
	Approvals []Approval
}

// Approval describes one scope requiring confirmation. It cannot override a deny.
type Approval struct {
	Reason  string
	RuleID  string
	Action  Action
	Pattern string // dangerous command fragment for display
}

// fileTools are tools that carry a single file path argument.
var fileTools = map[string]bool{
	"read": true, "write": true, "edit": true, "grep": true, "find": true, "ls": true,
}

func isKnownTool(name string) bool {
	return fileTools[name] || name == "bash" || name == "skill" || name == "tool_search" || name == "mcp_resource_list" || name == "mcp_server_info" || name == "tool_result_read" || name == "request_user_input" || isNetworkTool(name)
}

// networkTools reach outside the host through the web entry points and are
// checked as network actions instead of path or command actions.
func isNetworkTool(name string) bool {
	return name == "web_search" || name == "web_fetch"
}

func extractStringArgument(raw json.RawMessage, key string) string {
	var m map[string]any
	if err := jsonutil.DecodeStrict(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// extractActions returns the file actions of call and, for bash, its
// commands and the findings that need confirmation.
func extractActions(call llm.ToolCall, workspace string) bashAnalysis {
	if fileTools[call.Name] {
		p := extractFilePath(call.Arguments)
		if strings.TrimSpace(p) == "" {
			return bashAnalysis{}
		}
		op := opRead
		if call.Name == "write" || call.Name == "edit" {
			op = opWrite
		}
		return bashAnalysis{actions: []Action{{Kind: "file", Op: op, Path: p, ToolName: call.Name, Unresolved: containsExpansion(p)}}}
	}
	if call.Name == "bash" {
		if cmd := extractCommand(call.Arguments); strings.TrimSpace(cmd) != "" {
			return analyzeBash(cmd, workspace)
		}
	}
	return bashAnalysis{}
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

func maybePathLike(s string) bool {
	if s == "" {
		return false
	}
	// Heuristic: contains "/" or "."
	// or starts with "~". Also recognizes Windows "\" paths and drive letters
	// without treating a single escape like "\d" as a path.
	if strings.HasPrefix(s, "~") {
		return true
	}
	if strings.Contains(s, "/") {
		return true
	}
	if windowsPathLike(s) {
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

func windowsPathLike(s string) bool {
	if len(s) >= 2 && isDriveLetter(s[0]) && s[1] == ':' {
		return true
	}
	if !strings.Contains(s, `\`) {
		return false
	}
	if strings.HasPrefix(s, `\\`) {
		return true
	}
	i := strings.IndexByte(s, '\\')
	return i > 0 && i < len(s)-1
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
