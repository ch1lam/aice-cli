package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestLoopCompactionFailureStopsBeforeEffects(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"transient summary failure", "unchanged oversized context"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			info := testModel()
			info.ContextWindow = 4000
			service := &scriptedModel{}
			tool := newFakeTool("read", nil)
			loop := mustLoop(t, service, []agent.Tool{tool})
			input := testInput(info, mustPrompt(t, strings.Repeat("x", 14000)))
			calls := 0
			cause := &llm.ProviderError{StatusCode: 503, Err: errors.New("summary unavailable")}
			input.Compactor = func(_ context.Context, history []llm.AgentMessage) ([]llm.AgentMessage, error) {
				calls++
				if name == "transient summary failure" {
					return nil, cause
				}
				return history, nil
			}
			_, err := loop.Run(t.Context(), input, nil)
			if !errors.Is(err, agent.ErrContextLimit) {
				t.Fatalf("error=%v, want budget error", err)
			}
			if name == "transient summary failure" && !errors.Is(err, cause) {
				t.Fatalf("lost summary cause: %v", err)
			}
			if calls != 1 || len(service.requests) != 0 || len(tool.calls) != 0 {
				t.Fatalf("compactions/model/tools=%d/%d/%d", calls, len(service.requests), len(tool.calls))
			}
		})
	}
}

func TestLoopCompactsAfterPairedToolsAndSteeringBeforeRetry(t *testing.T) {
	t.Parallel()
	info := testModel()
	info.ContextWindow = 4000
	toolCall := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("read-1", "read", `{}`))
	// Fixture construction order differs from conversation order. Fixed
	// timestamps keep the usage applicable even across a wall-clock tick.
	toolCall.Timestamp = 2
	toolCall.Usage = llm.Usage{TotalTokens: 3500}
	failed := assistantMessage(info, llm.StopReasonError, textPart("do not replay retry failure"))
	failed.ErrorMessage = "temporary"
	final := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	service := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(toolCall)},
		{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: &llm.ProviderError{StatusCode: 503, Err: errors.New("temporary")}}}},
		{events: terminalEvents(final)},
	}}
	tool := newFakeTool("read", nil)
	loop, err := agent.NewLoop(service, []agent.Tool{tool}, agent.WithGuard(allowAllGuard{}), agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 1}))
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "inspect"))
	input.Prompt.Timestamp = 1
	steering := mustPrompt(t, "preserve this steering")
	steering.Timestamp = 3
	delivered := false
	input.Steering = func() (agent.InputMessage, bool, error) {
		if delivered {
			return agent.InputMessage{}, false, nil
		}
		delivered = true
		return agent.InputMessage{ID: "steer", Message: steering}, true, nil
	}
	compactions := 0
	input.Compactor = func(_ context.Context, history []llm.AgentMessage) ([]llm.AgentMessage, error) {
		compactions++
		if len(tool.calls) != 1 || len(history) != 4 {
			t.Fatalf("compaction before paired tool+steering: %#v", history)
		}
		if _, ok := history[2].(llm.ToolResultMessage); !ok {
			t.Fatal("unpaired group")
		}
		return []llm.AgentMessage{llm.CompactionSummaryMessage{Role: llm.RoleCompactionSummary, Summary: "read completed", TokensBefore: 3500, Timestamp: steering.Timestamp + 1}, history[3]}, nil
	}
	if _, err := loop.Run(t.Context(), input, nil); err != nil {
		t.Fatal(err)
	}
	if compactions != 1 || len(service.requests) != 3 || len(tool.calls) != 1 {
		t.Fatalf("compactions/model/tools=%d/%d/%d", compactions, len(service.requests), len(tool.calls))
	}
	for _, request := range service.requests[1:] {
		if len(request.Messages) != 2 || messageText(request.Messages[1]) != "preserve this steering" {
			t.Fatalf("retry context lost steering or replayed failed attempt: %#v", request.Messages)
		}
	}
}
