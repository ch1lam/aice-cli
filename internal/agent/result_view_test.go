package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestResultViewFollowsCompleteRecording(t *testing.T) {
	info := testModel()
	call := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("source", "large", `{}`))
	done := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(call)}, {events: terminalEvents(done)}}}
	text := strings.Repeat("source-result ", 10000)
	source := newFakeTool("large", func(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
		return llm.ToolResult{CallID: call.ID, Name: call.Name, Content: []llm.ContentPart{textPart(text)}, Details: &llm.ToolResultDetails{State: llm.ExecutionReturned}}, nil
	})
	input := testInput(info, mustPrompt(t, "read"))
	input.ResultViewTokens = 512
	var recorded []llm.AgentMessage
	input.MessageRecorder = captureMessages(&recorded)
	result, err := mustLoop(t, model, []agent.Tool{source}).Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelRounds[0].ToolResults[0].Content[0].Text != text || recorded[2].(llm.ToolResultMessage).Content[0].Text != text {
		t.Fatal("recording was trimmed")
	}
	view := model.requests[1].Messages[2].(llm.ToolResultMessage)
	if llm.EstimateMessageTokens(view) > 512 || !strings.Contains(view.Content[len(view.Content)-1].Text, "tool_result_read") {
		t.Fatal("next request did not use bounded view")
	}
	if len(source.calls) != 1 {
		t.Fatal("view retried source")
	}
}
