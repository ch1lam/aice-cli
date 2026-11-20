package agent_test

import (
	"context"
	"errors"
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
			notices := func(_ context.Context, result llm.ToolResultMessage) []agent.ToolResultNotice {
				calls++
				if result.ToolCallID != "call-1" {
					t.Errorf("notice source saw call %q", result.ToolCallID)
				}
				return []agent.ToolResultNotice{{Text: "task finished"}, {Text: "  "}}
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

// A notice's delegation is recorded on the result that carries it, also one
// without details of its own, and its usage counts toward the run's budget.
func TestLoopRecordsNoticeDelegations(t *testing.T) {
	t.Parallel()
	modelInfo := testModel()
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(assistantMessage(modelInfo, llm.StopReasonToolUse, toolCallPart("call-1", "probe", `{}`)))},
		{events: terminalEvents(assistantMessage(modelInfo, llm.StopReasonStop, textPart("done")))},
	}}
	notices := func(context.Context, llm.ToolResultMessage) []agent.ToolResultNotice {
		return []agent.ToolResultNotice{{Text: "subagent finished", Delegation: &llm.ToolDelegation{SessionID: "child", Usage: llm.Usage{TotalTokens: 30}}}}
	}
	loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("probe", nil)}, agent.WithGuard(allowAllGuard{}),
		agent.WithToolResultNotices(notices), agent.WithRunLimits(agent.RunLimits{Tokens: 30}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(t.Context(), testInput(modelInfo, mustPrompt(t, "probe")), nil)
	if !errors.Is(err, agent.ErrTokenBudget) || len(model.requests) != 1 {
		t.Fatalf("err = %v after %d requests, want the delegated usage to exhaust the budget", err, len(model.requests))
	}
	got := result.ModelRounds[0].ToolResults[0]
	if got.Details == nil || got.Details.State != llm.ExecutionReturned || len(got.Details.Delegations) != 1 ||
		got.Details.Delegations[0].SessionID != "child" || got.Content[0].Text != "subagent finished" {
		t.Fatalf("result = %#v / %#v", got.Content, got.Details)
	}
}
