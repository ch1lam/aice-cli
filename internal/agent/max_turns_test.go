package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestMaxTurnsSettlesLastToolBatchBeforeStopping(t *testing.T) {
	t.Parallel()
	info := testModel()
	info.ContextWindow = 4000
	call := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("a", "read", `{}`), toolCallPart("b", "read", `{}`))
	call.Usage = llm.Usage{TotalTokens: 3500}
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(call)}}}
	tool := newFakeTool("read", nil)
	loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{MaxTurns: 1}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "inspect"))
	input.Compactor = func(context.Context, []llm.AgentMessage) (agent.CompactionResult, error) {
		t.Fatal("compaction started after turn limit")
		return agent.CompactionResult{}, nil
	}
	var recorded []llm.AgentMessage
	input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
		recorded = append(recorded, message)
		return nil
	}
	var ended error
	result, err := loop.Run(t.Context(), input, func(_ context.Context, event agent.AgentEvent) error {
		if event.Type == agent.EventTypeAgentEnd {
			ended = event.Err
		}
		return nil
	})
	if !errors.Is(err, agent.ErrMaxTurns) || !errors.Is(ended, agent.ErrMaxTurns) {
		t.Fatalf("run/end=%v/%v", err, ended)
	}
	if len(model.requests) != 1 || len(tool.calls) != 2 {
		t.Fatalf("requests/tools=%d/%d", len(model.requests), len(tool.calls))
	}
	for _, r := range result.ModelRounds[0].ToolResults {
		if r.IsError {
			t.Fatal("last permitted tool batch was skipped")
		}
	}
	if len(result.ModelRounds) != 2 || len(recorded) != len(result.Messages()) || !strings.Contains(result.ModelRounds[1].Assistant.ErrorMessage, "maximum model turns") {
		t.Fatal("missing durable terminal reason or duplicated finalization")
	}
}

func TestMaxTurnsCountsFailedModelAttempts(t *testing.T) {
	t.Parallel()
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw stream failure", true: "terminal failure"}[terminal], func(t *testing.T) {
			info := testModel()
			script := &streamScript{events: []llm.Event{{Type: llm.EventTypeStart}}, nextErr: io.ErrUnexpectedEOF}
			if terminal {
				failed := assistantMessage(info, llm.StopReasonError, textPart("temporary"))
				failed.ErrorMessage = "temporary"
				script = &streamScript{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: io.ErrUnexpectedEOF}}}
			}
			model := &scriptedModel{scripts: []*streamScript{script}}
			loop, err := agent.NewLoop(model, nil, agent.WithRunLimits(agent.RunLimits{MaxTurns: 1}), agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 3}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "inspect")), nil)
			if !errors.Is(err, agent.ErrMaxTurns) || len(model.requests) != 1 {
				t.Fatalf("err=%v requests=%d", err, len(model.requests))
			}
			if !strings.Contains(result.ModelRounds[len(result.ModelRounds)-1].Assistant.ErrorMessage, "maximum model turns") {
				t.Fatal("lost limit reason after failure")
			}
		})
	}
}

func TestMaxTurnsSpansInputsAndResetsForNewRun(t *testing.T) {
	t.Parallel()
	for _, steering := range []bool{false, true} {
		t.Run(map[bool]string{false: "follow-up", true: "steering"}[steering], func(t *testing.T) {
			info := testModel()
			answer := assistantMessage(info, llm.StopReasonStop, textPart("done"))
			model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(answer)}, {events: terminalEvents(answer)}}}
			loop, err := agent.NewLoop(model, nil, agent.WithRunLimits(agent.RunLimits{MaxTurns: 1}))
			if err != nil {
				t.Fatal(err)
			}
			input := testInput(info, mustPrompt(t, "first"))
			source := func() (agent.InputMessage, bool, error) {
				return agent.InputMessage{ID: "next", Message: mustPrompt(t, "second")}, true, nil
			}
			if steering {
				input.Steering = source
			} else {
				input.FollowUp = source
			}
			result, err := loop.Run(t.Context(), input, nil)
			if !errors.Is(err, agent.ErrMaxTurns) || len(model.requests) != 1 {
				t.Fatalf("err=%v requests=%d", err, len(model.requests))
			}
			last := result.ModelRounds[len(result.ModelRounds)-1]
			if len(last.Inputs) != 1 {
				t.Fatal("accepted input was lost")
			}
			next := testInput(info, mustPrompt(t, "fresh"))
			next.History = result.Messages()
			if _, err := loop.Run(t.Context(), next, nil); err != nil {
				t.Fatal("final answer at limit should succeed in fresh run", err)
			}
		})
	}
}
