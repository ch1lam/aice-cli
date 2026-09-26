package anthropic

import (
	"fmt"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

// Native OAuth wire compatibility reference: pi-mono
// d6af72e1857cfb10b41d8ff8e69f0d72b4cf6d31, packages/ai/src/api/anthropic-messages.ts.
// Keep this exception isolated from ordinary API-key clients.
const subscriptionUserAgent = "claude-cli/2.1.280"
const subscriptionIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

func subscriptionToolName(name string) string {
	for _, canonical := range []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion",
		"EnterPlanMode", "ExitPlanMode", "KillShell", "NotebookEdit", "Skill", "Task", "TaskOutput", "TodoWrite", "WebFetch", "WebSearch"} {
		if strings.EqualFold(name, canonical) {
			return canonical
		}
	}
	return name
}

// Only SDK-owned request values change. Session tool names remain canonical
// AICE names, and stream events map the response names back before Guard sees them.
func subscriptionParams(params *anthropicsdk.MessageNewParams) (map[string]string, error) {
	params.System = append([]anthropicsdk.TextBlockParam{{Text: subscriptionIdentity}}, params.System...)
	names := make(map[string]string)
	for i := range params.Tools {
		tool := params.Tools[i].OfTool
		if tool == nil {
			continue
		}
		original, wire := tool.Name, subscriptionToolName(tool.Name)
		if previous, exists := names[wire]; exists && previous != original {
			return nil, fmt.Errorf("anthropic: subscription tool names %q and %q collide", previous, original)
		}
		names[wire] = original
		tool.Name = wire
	}
	for i := range params.Messages {
		for j := range params.Messages[i].Content {
			if call := params.Messages[i].Content[j].OfToolUse; call != nil {
				call.Name = subscriptionToolName(call.Name)
			}
		}
	}
	return names, nil
}
