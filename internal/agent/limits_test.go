package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestRunTokenBudgetStopsBeforeToolsAndPreservesPairs(t *testing.T) {
	t.Parallel()
	info := testModel()
	call := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("a", "read", `{}`), toolCallPart("b", "read", `{}`))
	call.Usage = llm.Usage{TotalTokens: 10}
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(call)}}}
	tool := newFakeTool("read", nil)
	loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{Tokens: 10}))
	if err != nil {
		t.Fatal(err)
	}
	var recorded []llm.AgentMessage
	input := testInput(info, mustPrompt(t, "inspect"))
	input.MessageRecorder = func(_ context.Context, m llm.AgentMessage) error { recorded = append(recorded, m); return nil }
	var terminal error
	result, err := loop.Run(t.Context(), input, func(_ context.Context, event agent.AgentEvent) error {
		if event.Type == agent.EventTypeAgentEnd {
			terminal = event.Err
		}
		return nil
	})
	if !errors.Is(err, agent.ErrTokenBudget) || !errors.Is(terminal, agent.ErrTokenBudget) {
		t.Fatalf("run/end errors = %v / %v", err, terminal)
	}
	if len(tool.calls) != 0 || len(model.requests) != 1 {
		t.Fatal("effects after budget exhaustion")
	}
	if len(result.ModelRounds[0].ToolResults) != 2 {
		t.Fatal("tool calls were not paired")
	}
	for _, r := range result.ModelRounds[0].ToolResults {
		if !r.IsError {
			t.Fatal("skipped call was not an error")
		}
	}
	if len(recorded) != len(result.Messages()) || result.Usage.TotalTokens != 10 {
		t.Fatalf("recording/usage = %d / %+v", len(recorded), result.Usage)
	}
	if !strings.Contains(result.ModelRounds[len(result.ModelRounds)-1].Assistant.ErrorMessage, "token budget") {
		t.Fatal("missing durable budget reason")
	}
}

func TestRunBudgetCountsFailedAttemptsWithoutDoubleCountingUsage(t *testing.T) {
	t.Parallel()
	for _, rawFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "terminal failure", true: "stream failure"}[rawFailure], func(t *testing.T) {
			info := testModel()
			usage := llm.Usage{TotalTokens: 10}
			failed := assistantMessage(info, llm.StopReasonError, textPart("temporary"))
			failed.ErrorMessage = "temporary"
			failed.Usage = usage
			events := []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeUsage, Usage: &usage}}
			script := &streamScript{events: events}
			if rawFailure {
				script.nextErr = io.ErrUnexpectedEOF
			} else {
				script.events = append(events, llm.Event{Type: llm.EventTypeError, Message: &failed, Err: &llm.ProviderError{StatusCode: 503, Err: errors.New("temporary")}})
			}
			model := &scriptedModel{scripts: []*streamScript{script}}
			loop, err := agent.NewLoop(model, nil, agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 1}), agent.WithRunLimits(agent.RunLimits{Tokens: 10}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "test")), nil)
			if !errors.Is(err, agent.ErrTokenBudget) || len(model.requests) != 1 || result.Usage.TotalTokens != 10 {
				t.Fatalf("err=%v requests=%d usage=%+v", err, len(model.requests), result.Usage)
			}
		})
	}
}

func TestRunBudgetIncludesCompaction(t *testing.T) {
	t.Parallel()
	info := testModel()
	info.ContextWindow = 4000
	model := &scriptedModel{}
	loop, err := agent.NewLoop(model, nil, agent.WithRunLimits(agent.RunLimits{Tokens: 10}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, strings.Repeat("x", 14000)))
	input.Compactor = func(context.Context, []llm.AgentMessage) (agent.CompactionResult, error) {
		return agent.CompactionResult{History: []llm.AgentMessage{mustPrompt(t, "small")}, Usage: llm.Usage{TotalTokens: 10}}, nil
	}
	result, err := loop.Run(t.Context(), input, nil)
	if !errors.Is(err, agent.ErrTokenBudget) || len(model.requests) != 0 || result.Usage.TotalTokens != 10 {
		t.Fatalf("err=%v requests=%d usage=%+v", err, len(model.requests), result.Usage)
	}
}

func TestRunBudgetSpansFollowUpsButResetsForNewRun(t *testing.T) {
	t.Parallel()
	info := testModel()
	answer := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	answer.Usage = llm.Usage{TotalTokens: 10}
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(answer)}, {events: terminalEvents(answer)}}}
	loop, err := agent.NewLoop(model, nil, agent.WithRunLimits(agent.RunLimits{Tokens: 10}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "first"))
	input.FollowUp = func() (agent.InputMessage, bool, error) {
		return agent.InputMessage{ID: "next", Message: mustPrompt(t, "second")}, true, nil
	}
	if _, err = loop.Run(t.Context(), input, nil); !errors.Is(err, agent.ErrTokenBudget) {
		t.Fatal(err)
	}
	if _, err = loop.Run(t.Context(), testInput(info, mustPrompt(t, "fresh")), nil); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("requests=%d", len(model.requests))
	}
}

func TestRunTimeBudgetCancelsToolAndPairsRemainder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		info := testModel()
		call := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("a", "read", `{}`), toolCallPart("b", "read", `{}`))
		model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(call)}}}
		tool := newFakeTool("read", func(ctx context.Context, _ llm.ToolCall) (llm.ToolResult, error) {
			<-ctx.Done()
			return llm.ToolResult{}, ctx.Err()
		})
		loop, err := agent.NewLoop(model, []agent.Tool{tool}, agent.WithGuard(allowAllGuard{}), agent.WithRunLimits(agent.RunLimits{Timeout: time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := loop.Run(t.Context(), testInput(info, mustPrompt(t, "test")), nil)
		if !errors.Is(err, agent.ErrTimeBudget) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if len(tool.calls) != 1 || len(result.ModelRounds[0].ToolResults) != 2 {
			t.Fatalf("calls=%d result=%+v", len(tool.calls), result)
		}
		if !strings.Contains(result.ModelRounds[len(result.ModelRounds)-1].Assistant.ErrorMessage, "time budget") {
			t.Fatal("missing time budget reason")
		}
	})
}
