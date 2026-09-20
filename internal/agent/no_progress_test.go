package agent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestNoProgressStopsEquivalentCallsAndKeepsActualResults(t *testing.T) {
	t.Parallel()
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful repeated read", true: "repeated denial"}[denied], func(t *testing.T) {
			info := testModel()
			model := repeatedToolModel(info, []string{`{"a":1,"nested":{"b":2,"c":3}}`, `{ "nested": { "c":3, "b":2 }, "a":1 }`, `{"a":1,"nested":{"b":2,"c":3}}`})
			tool := newFakeTool("read", nil)
			var guard agent.Guard = allowAllGuard{}
			if denied {
				guard = fixedDecisionGuard{result: agent.GuardResult{Decision: agent.GuardDeny, Reason: "denied"}}
			}
			loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(guard), agent.WithRunLimits(agent.RunLimits{NoProgress: 3}))
			if err != nil {
				t.Fatal(err)
			}
			var recorded []llm.AgentMessage
			input := testInput(info, mustPrompt(t, "inspect"))
			input.MessageRecorder = func(_ context.Context, m llm.AgentMessage) error { recorded = append(recorded, m); return nil }
			result, err := loop.Run(t.Context(), input, nil)
			if !errors.Is(err, agent.ErrNoProgress) || len(model.requests) != 3 {
				t.Fatalf("err=%v requests=%d", err, len(model.requests))
			}
			for _, round := range result.ModelRounds[:3] {
				if len(round.ToolResults) != 1 || round.ToolResults[0].IsError != denied {
					t.Fatalf("lost actual outcome: %+v", round)
				}
			}
			if len(recorded) != len(result.Messages()) || !strings.Contains(result.ModelRounds[3].Assistant.ErrorMessage, "no observable progress") {
				t.Fatal("stop reason was not recorded")
			}
			if denied && len(tool.calls) != 0 {
				t.Fatal("guard denial bypassed")
			}
		})
	}
}

func TestNoProgressAllowsChangingWorkAndCanBeDisabled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		limit          int
		changingOutput bool
		args           []string
	}{
		{"disabled", 0, false, []string{`{}`, `{}`, `{}`}},
		{"changing output", 2, true, []string{`{}`, `{}`, `{}`}},
		{"changing arguments", 2, false, []string{`{"offset":1}`, `{"offset":2}`, `{"offset":3}`}},
		{"large integers remain distinct", 2, false, []string{`{"n":9007199254740992}`, `{"n":9007199254740993}`, `{"n":9007199254740992}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := testModel()
			model := repeatedToolModel(info, tc.args)
			n := 0
			tool := newFakeTool("read", func(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error) {
				result, err := successfulTool(ctx, call)
				if tc.changingOutput {
					n++
					result.Content = []llm.ContentPart{textPart(fmt.Sprint(n))}
				}
				return result, err
			})
			loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{NoProgress: tc.limit}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "inspect")), nil); err != nil {
				t.Fatal(err)
			}
			if len(tool.calls) != 3 || len(model.requests) != 4 {
				t.Fatal("work stopped prematurely")
			}
		})
	}
}

func TestNoProgressSteeringResetsStreak(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := repeatedToolModel(info, []string{`{}`, `{}`, `{}`})
	loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("read", nil)}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{NoProgress: 2}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "inspect"))
	polls := 0
	input.Steering = func() (agent.InputMessage, bool, error) {
		polls++
		if polls == 2 {
			return agent.InputMessage{ID: "steer", Message: mustPrompt(t, "read again now")}, true, nil
		}
		return agent.InputMessage{}, false, nil
	}
	if _, err := loop.Run(t.Context(), input, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNoProgressSurvivesCompaction(t *testing.T) {
	t.Parallel()
	info := testModel()
	info.ContextWindow = 4000
	model := repeatedToolModel(info, []string{`{}`, `{}`, `{}`})
	for _, script := range model.scripts[:3] {
		script.events[len(script.events)-1].Message.Usage = llm.Usage{TotalTokens: 3500}
		script.events[len(script.events)-1].Message.Timestamp = 2
	}
	loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("read", nil)}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{NoProgress: 3}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "inspect"))
	input.Prompt.Timestamp = 1
	compactions := 0
	input.Compactor = func(context.Context, []llm.AgentMessage) (agent.CompactionResult, error) {
		compactions++
		summary := mustPrompt(t, "continue")
		summary.Timestamp = 1
		return agent.CompactionResult{History: []llm.AgentMessage{summary}}, nil
	}
	if _, err := loop.Run(t.Context(), input, nil); !errors.Is(err, agent.ErrNoProgress) {
		t.Fatal(err)
	}
	if compactions != 2 || len(model.requests) != 3 {
		t.Fatalf("compactions=%d requests=%d", compactions, len(model.requests))
	}
}

func TestNoProgressResetsForFollowUpAndNewRun(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := repeatedToolModel(info, []string{`{}`})
	model.scripts = append(model.scripts, repeatedToolModel(info, []string{`{}`}).scripts...)
	model.scripts = append(model.scripts, repeatedToolModel(info, []string{`{}`}).scripts...)
	loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("read", nil)}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{NoProgress: 2}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "inspect"))
	followed := false
	input.FollowUp = func() (agent.InputMessage, bool, error) {
		if followed {
			return agent.InputMessage{}, false, nil
		}
		followed = true
		return agent.InputMessage{ID: "follow", Message: mustPrompt(t, "inspect again")}, true, nil
	}
	result, err := loop.Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := testInput(info, mustPrompt(t, "continue"))
	next.History = result.Messages()
	if _, err := loop.Run(t.Context(), next, nil); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 6 {
		t.Fatalf("requests=%d", len(model.requests))
	}
}

func repeatedToolModel(info llm.Model, args []string) *scriptedModel {
	model := &scriptedModel{}
	for index, arg := range args {
		call := assistantMessage(info, llm.StopReasonToolUse, textPart(fmt.Sprintf("different commentary %d", index)), toolCallPart(fmt.Sprint(index), "read", arg))
		model.scripts = append(model.scripts, &streamScript{events: terminalEvents(call)})
	}
	final := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	model.scripts = append(model.scripts, &streamScript{events: terminalEvents(final)})
	return model
}
