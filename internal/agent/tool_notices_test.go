package agent_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Notices reach every view of a result entering context, including Guard
// denials, and never replace the tool's own content or error state.
func TestLoopPrependsToolResultNotices(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		guard     agent.Guard
		wantError bool
		wantTail  string
	}{
		{name: "executed", guard: allowAllGuard{}, wantTail: "ok"},
		{name: "denied", guard: fixedDecisionGuard{result: agent.GuardResult{
			Decision: agent.GuardDeny, Reason: "blocked", RuleID: "test",
		}}, wantError: true, wantTail: "blocked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			modelInfo := testModel()
			model := &scriptedModel{scripts: []*streamScript{
				{events: terminalEvents(assistantMessage(modelInfo, llm.StopReasonToolUse,
					toolCallPart("call-1", "probe", `{}`)))},
				{events: terminalEvents(assistantMessage(modelInfo, llm.StopReasonStop, textPart("done")))},
			}}
			calls := 0
			notices := func(_ context.Context, result llm.ToolResultMessage) []string {
				calls++
				if result.ToolCallID != "call-1" {
					t.Errorf("notice source saw call %q", result.ToolCallID)
				}
				return []string{"task finished", "  "}
			}
			loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("probe", nil)},
				agent.WithGuard(test.guard), agent.WithToolResultNotices(notices))
			if err != nil {
				t.Fatal(err)
			}
			input := testInput(modelInfo, mustPrompt(t, "probe"))
			var recorded []llm.ToolResultMessage
			input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
				if result, ok := message.(llm.ToolResultMessage); ok {
					recorded = append(recorded, result)
				}
				return nil
			}
			var events []agent.AgentEvent
			result, err := loop.Run(t.Context(), input, collectEvents(&events))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if calls != 1 || len(recorded) != 1 {
				t.Fatalf("notice calls = %d, recorded = %d; want 1 each", calls, len(recorded))
			}
			got := recorded[0]
			if got.IsError != test.wantError || len(got.Content) != 2 ||
				got.Content[0].Text != "task finished" || got.Content[1].Text != test.wantTail {
				t.Fatalf("recorded result = %#v", got)
			}
			request := model.requests[1]
			if !reflect.DeepEqual(request.Messages[len(request.Messages)-1], got) ||
				!reflect.DeepEqual(result.ModelRounds[0].ToolResults, []llm.ToolResultMessage{got}) {
				t.Fatal("model context or retained round differs from recorded result")
			}
			for _, event := range events {
				if event.Type == agent.EventTypeToolExecutionEnd && !reflect.DeepEqual(*event.ToolResult, got) {
					t.Fatalf("tool_execution_end result = %#v", *event.ToolResult)
				}
			}
		})
	}
}
