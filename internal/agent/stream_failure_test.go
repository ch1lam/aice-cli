package agent_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestLoopDoesNotAcceptToolDeltasWithoutTerminalMessage(t *testing.T) {
	t.Parallel()
	failure := errors.New("stream transport failed")
	for _, args := range []struct{ name, delta string }{
		{"incomplete JSON", `{"path":`},
		{"valid JSON still streaming", `{"path":"a.go"}`},
	} {
		t.Run(args.name, func(t *testing.T) {
			t.Parallel()
			for _, ending := range []struct {
				name             string
				nextErr, wantErr error
				cancel           bool
				stop             llm.StopReason
			}{
				{name: "EOF", nextErr: io.EOF, wantErr: agent.ErrProtocol, stop: llm.StopReasonError},
				{name: "transport failure", nextErr: failure, wantErr: failure, stop: llm.StopReasonError},
				{name: "cancel then EOF", nextErr: io.EOF, wantErr: context.Canceled, cancel: true, stop: llm.StopReasonAborted},
			} {
				t.Run(ending.name, func(t *testing.T) {
					t.Parallel()
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					info := testModel()
					script := &streamScript{
						events: []llm.Event{
							{Type: llm.EventTypeStart},
							{Type: llm.EventTypeToolCallStart, ContentIndex: 0, ToolCallDelta: &llm.ToolCallDelta{ID: "call-1", Name: "read"}},
							{Type: llm.EventTypeToolCallDelta, ContentIndex: 0, ToolCallDelta: &llm.ToolCallDelta{ArgumentsDelta: args.delta}},
						},
						nextErr: ending.nextErr,
						onNext: func(index int) {
							if ending.cancel && index == 2 {
								cancel()
							}
						},
					}
					provider := &scriptedModel{scripts: []*streamScript{script}}
					tool := newFakeTool("read", nil)
					input := testInput(info, mustPrompt(t, "inspect"))
					var recorded []llm.AgentMessage
					input.MessageRecorder = captureMessages(&recorded)
					var events []agent.AgentEvent
					result, err := mustLoop(t, provider, []agent.Tool{tool},
						agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 0}),
					).Run(ctx, input, collectEvents(&events))
					if !errors.Is(err, ending.wantErr) {
						t.Fatalf("Run error = %v, want %v", err, ending.wantErr)
					}
					if len(tool.calls) != 0 || len(provider.requests) != 1 || !script.closed {
						t.Fatalf("calls=%d requests=%d closed=%v", len(tool.calls), len(provider.requests), script.closed)
					}
					if len(result.ModelRounds) != 1 || len(result.ModelRounds[0].ToolResults) != 0 {
						t.Fatalf("unaccepted deltas became tool history: %#v", result.ModelRounds)
					}
					assertTerminalFailure(t, result.ModelRounds[0].Assistant, ending.stop)
					assertRecordedMessages(t, recorded, result.Messages())
					assertFailedStreamEvents(t, events, result, ending.wantErr)
				})
			}
		})
	}
}

func TestLoopPairsAcceptedToolCallsWithoutExecutionAfterStreamFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("provider failed after tool delta")
	for _, test := range []struct {
		name                 string
		cancel, closeFailure bool
	}{
		{name: "terminal provider error"},
		{name: "terminal cancellation", cancel: true},
		{name: "close failure after done", closeFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			info := testModel()
			stop, terminalType, wantErr := llm.StopReasonError, llm.EventTypeError, failure
			if test.cancel {
				stop, wantErr = llm.StopReasonAborted, context.Canceled
			}
			if test.closeFailure {
				stop, terminalType = llm.StopReasonToolUse, llm.EventTypeDone
			}
			assistant := assistantMessage(info, stop, textPart("safe partial output"), toolCallPart("call-1", "read", `{"path":"a.go"}`))
			terminal := llm.Event{Type: terminalType, Message: &assistant, StopReason: stop}
			if !test.closeFailure {
				assistant.ErrorMessage = wantErr.Error()
				terminal.Err = wantErr
			}
			call := assistant.Content[1].ToolCall
			script := &streamScript{events: []llm.Event{
				{Type: llm.EventTypeStart},
				{Type: llm.EventTypeTextDelta, ContentIndex: 0, Delta: "safe partial output"},
				{Type: llm.EventTypeToolCallStart, ContentIndex: 1, ToolCallDelta: &llm.ToolCallDelta{ID: call.ID, Name: call.Name}},
				{Type: llm.EventTypeToolCallDelta, ContentIndex: 1, ToolCallDelta: &llm.ToolCallDelta{ArgumentsDelta: string(call.Arguments)}},
				terminal,
			}, onNext: func(index int) {
				if test.cancel && index == 4 {
					cancel()
				}
			}}
			if test.closeFailure {
				script.closeErr = failure
			}
			provider := &scriptedModel{scripts: []*streamScript{script}}
			tool := newFakeTool("read", nil)
			input := testInput(info, mustPrompt(t, "inspect"))
			var recorded []llm.AgentMessage
			input.MessageRecorder = captureMessages(&recorded)
			var events []agent.AgentEvent
			result, err := mustLoop(t, provider, []agent.Tool{tool}, agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 0})).Run(ctx, input, collectEvents(&events))
			if !errors.Is(err, wantErr) {
				t.Fatalf("Run error = %v, want %v", err, wantErr)
			}
			if len(tool.calls) != 0 || len(provider.requests) != 1 || !script.closed {
				t.Fatalf("calls=%d requests=%d closed=%v", len(tool.calls), len(provider.requests), script.closed)
			}
			if len(result.ModelRounds) != 2 || len(result.ModelRounds[0].ToolResults) != 1 {
				t.Fatalf("missing paired failure and terminal assistant: %#v", result.ModelRounds)
			}
			assertAgentMessage(t, result.ModelRounds[0].Assistant, assistant)
			paired := result.ModelRounds[0].ToolResults[0]
			if paired.ToolCallID != call.ID || paired.ToolName != call.Name || !paired.IsError || messageText(paired) == "" {
				t.Fatalf("paired result = %#v", paired)
			}
			finalStop := llm.StopReasonError
			if test.cancel {
				finalStop = llm.StopReasonAborted
			}
			assertTerminalFailure(t, result.ModelRounds[1].Assistant, finalStop)
			assertRecordedMessages(t, recorded, result.Messages())
			assertFailedStreamEvents(t, events, result, wantErr)
		})
	}
}

func assertFailedStreamEvents(t *testing.T, events []agent.AgentEvent, result agent.Result, wantErr error) {
	t.Helper()
	ends, deltas := 0, 0
	for _, event := range events {
		switch event.Type {
		case agent.EventTypeToolExecutionStart, agent.EventTypeToolExecutionEnd, agent.EventTypeInteractionEnd:
			t.Fatalf("failed stream emitted %s", event.Type)
		case agent.EventTypeMessageUpdate:
			if event.AssistantMessageEvent != nil && event.AssistantMessageEvent.Type == llm.EventTypeToolCallDelta {
				deltas++
			}
		case agent.EventTypeAgentEnd:
			ends++
		}
	}
	if ends != 1 || deltas != 1 {
		t.Fatalf("agent ends=%d tool deltas=%d", ends, deltas)
	}
	last := events[len(events)-1]
	if last.Type != agent.EventTypeAgentEnd || !errors.Is(last.Err, wantErr) || !reflect.DeepEqual(last.Messages, result.Messages()) {
		t.Fatalf("terminal event does not retain failure and history: %#v", last)
	}
}
