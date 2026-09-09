package agent_test

import (
	"context"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestToolDiffReachesRecorderEventsAndNextRequest(t *testing.T) {
	info := testModel()
	first := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("edit-1", "edit", `{}`))
	last := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(first)}, {events: terminalEvents(last)}}}
	want := llm.ToolDiff{Text: "@@ -1 +1 @@\n-old\n+new\n", Truncated: true}
	tool := newFakeTool("edit", func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		return llm.ToolResult{Content: []llm.ContentPart{textPart("Edited.")}, Diff: want}, nil
	})
	input := testInput(info, mustPrompt(t, "edit"))
	var recorded llm.ToolResultMessage
	input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
		if result, ok := message.(llm.ToolResultMessage); ok {
			recorded = result
		}
		return nil
	}
	seen := false
	_, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input, func(_ context.Context, event agent.AgentEvent) error {
		if event.Type == agent.EventTypeToolExecutionEnd {
			seen = true
			if event.ToolResult.Diff != want || recorded.Diff != want {
				t.Fatal("metadata lost before tool-end display")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no tool-end event")
	}
	messages := model.requests[1].Messages
	if messages[len(messages)-1].(llm.ToolResultMessage).Diff != want {
		t.Fatal("continuation lost metadata")
	}
}
