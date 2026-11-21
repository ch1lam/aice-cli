package agent_test

import (
	"context"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// A tool reads the history its calls' request was built from: everything
// before their assistant message, unchanged by results that follow.
func TestRequestHistoryIsTheCallingRequest(t *testing.T) {
	t.Parallel()
	if _, err := agent.RequestHistory(t.Context()); err == nil {
		t.Fatal("history outside a tool call")
	}
	info := testModel()
	first := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("one", "probe", `{}`), toolCallPart("two", "probe", `{}`))
	second := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("three", "probe", `{}`))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(first)}, {events: terminalEvents(second)},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}}
	var seen []int
	probe := newFakeTool("probe", func(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
		history, err := agent.RequestHistory(ctx)
		if err != nil {
			t.Error(err)
		}
		seen = append(seen, len(history))
		return successfulTool(ctx, call)
	})
	if _, err := mustLoop(t, model, []agent.Tool{probe}).Run(t.Context(), testInput(info, mustPrompt(t, "work")), nil); err != nil {
		t.Fatal(err)
	}
	// The prompt; then the prompt, the first assistant and its two results.
	if len(seen) != 3 || seen[0] != 1 || seen[1] != 1 || seen[2] != 4 {
		t.Fatalf("history lengths = %v", seen)
	}
}
