package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestLoopRevalidatesAfterGuardApproval(t *testing.T) {
	t.Parallel()
	for _, decision := range []agent.GuardDecision{agent.GuardAllow, agent.GuardAsk, agent.GuardDeny} {
		t.Run(string(decision), func(t *testing.T) {
			t.Parallel()
			approved, validated := false, false
			guardResult := agent.GuardResult{Decision: decision, Revalidate: func(context.Context) error {
				validated = true
				if decision == agent.GuardAsk && !approved {
					t.Error("validated before approval")
				}
				return errors.New("target changed")
			}}
			if decision == agent.GuardAsk {
				guardResult.Approvals = []agent.GuardApproval{{RuleID: "path", Reason: "outside"}}
			}
			info := testModel()
			model := &scriptedModel{scripts: []*streamScript{
				{events: terminalEvents(assistantMessage(info, llm.StopReasonToolUse, toolCallPart("call-1", "write", `{"path":"alias","content":"new"}`)))},
				{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
			}}
			writer := newFakeTool("write", successfulTool)
			loop := mustLoop(t, model, []agent.Tool{writer}, agent.WithGuard(fixedDecisionGuard{result: guardResult}), agent.WithGuardAskHandler(func(context.Context, llm.ToolCall, agent.GuardApproval) (agent.GuardAskReply, error) {
				approved = true
				return agent.GuardAskReply{Decision: agent.GuardAllow}, nil
			}))
			result, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "write")), nil)
			if err != nil {
				t.Fatal(err)
			}
			if validated != (decision != agent.GuardDeny) {
				t.Fatalf("validated = %v", validated)
			}
			if len(writer.calls) != 0 {
				t.Fatal("tool executed despite rejection")
			}
			if len(result.ModelRounds) == 0 || len(result.ModelRounds[0].ToolResults) != 1 || !result.ModelRounds[0].ToolResults[0].IsError {
				t.Fatalf("missing paired error: %+v", result)
			}
		})
	}
}
