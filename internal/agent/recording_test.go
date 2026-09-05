package agent_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestMessageRecorderFailureStopsLaterEffectsWithoutCleanupRetry(t *testing.T) {
	t.Parallel()
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			t.Parallel()
			info := testModel()
			assistant := assistantMessage(info, llm.StopReasonToolUse,
				toolCallPart("one", "write", `{}`), toolCallPart("two", "write", `{}`))
			model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(assistant)}}}
			tool := newFakeTool("write", nil)
			input := testInput(info, mustPrompt(t, "work"))
			failure := errors.New("disk unavailable")
			calls := 0
			input.MessageRecorder = func(context.Context, llm.AgentMessage) error {
				calls++
				if calls >= failAt {
					return failure
				}
				return nil
			}
			result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input, nil)
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if calls != failAt {
				t.Fatalf("recorder called %d times, want %d", calls, failAt)
			}
			wantRequests := 1
			if failAt == 1 {
				wantRequests = 0
			}
			if len(model.requests) != wantRequests {
				t.Fatalf("requests = %d", len(model.requests))
			}
			wantTools := max(0, failAt-2)
			if len(tool.calls) != wantTools {
				t.Fatalf("tools = %d, want %d", len(tool.calls), wantTools)
			}
			if failAt >= 2 {
				assertAgentMessage(t, result.ModelRounds[0].Assistant, assistant)
			}
			if failAt >= 3 && result.ModelRounds[0].ToolResults[0].IsError {
				t.Fatal("actual completed result was replaced by synthetic failure")
			}
		})
	}
}

func TestMessageRecorderSurvivesDisplayFailureAndKeepsTerminalAssistant(t *testing.T) {
	t.Parallel()
	for _, failurePoint := range []agent.EventType{agent.EventTypeMessageEnd, agent.EventTypeToolExecutionEnd} {
		t.Run(string(failurePoint), func(t *testing.T) {
			t.Parallel()
			info := testModel()
			assistant := assistantMessage(info, llm.StopReasonToolUse,
				toolCallPart("one", "write", `{}`), toolCallPart("two", "write", `{}`))
			model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(assistant)}}}
			tool := newFakeTool("write", nil)
			input := testInput(info, mustPrompt(t, "work"))
			var recorded []llm.AgentMessage
			input.MessageRecorder = captureMessages(&recorded)
			displayErr := errors.New("terminal closed")
			result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input,
				func(_ context.Context, event agent.AgentEvent) error {
					if event.Type == failurePoint && (failurePoint != agent.EventTypeMessageEnd || event.Message.MessageRole() == llm.RoleAssistant) {
						// The callback already saw the ended message or actual tool result.
						last := recorded[len(recorded)-1]
						if failurePoint == agent.EventTypeToolExecutionEnd {
							assertAgentMessage(t, last, *event.ToolResult)
						} else {
							assertAgentMessage(t, last, assistant)
						}
						return displayErr
					}
					return nil
				})
			if !errors.Is(err, displayErr) {
				t.Fatalf("error = %v", err)
			}
			assertAgentMessage(t, result.ModelRounds[0].Assistant, assistant)
			assertRecordedMessages(t, recorded, result.Messages())
			wantExecuted := 0
			if failurePoint == agent.EventTypeToolExecutionEnd {
				wantExecuted = 1
			}
			if len(tool.calls) != wantExecuted || len(model.requests) != 1 {
				t.Fatalf("tools/requests = %d/%d", len(tool.calls), len(model.requests))
			}
		})
	}
}

func TestMessageRecorderCapturesInputsAndDoesNotRepeatHistory(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("first")))},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("second")))},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("third")))},
	}}
	input := testInput(info, mustPrompt(t, "initial"))
	input.History = []llm.AgentMessage{mustPrompt(t, "old"), assistantMessage(info, llm.StopReasonStop, textPart("old answer"))}
	once := func(text string) agent.InputSource {
		delivered := false
		return func() (agent.InputMessage, bool, error) {
			if delivered {
				return agent.InputMessage{}, false, nil
			}
			delivered = true
			return agent.InputMessage{ID: text, Message: mustPrompt(t, text)}, true, nil
		}
	}
	input.Steering = once("steer")
	input.FollowUp = once("follow")
	var recorded []llm.AgentMessage
	input.MessageRecorder = captureMessages(&recorded)
	result, err := mustLoop(t, model, nil).Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertRecordedMessages(t, recorded, result.Messages())
	if len(recorded) != 6 {
		t.Fatalf("recorded = %d, want 6", len(recorded))
	}
}

func TestMessageRecorderIncludesFailedRetryAndSyntheticResults(t *testing.T) {
	t.Parallel()
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			t.Parallel()
			info := testModel()
			failure := &llm.ProviderError{StatusCode: 503, Err: errors.New("unavailable")}
			var service agent.Model
			success := &streamScript{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))}
			if started {
				failed := assistantMessage(info, llm.StopReasonError, toolCallPart("failed", "write", `{}`))
				failed.ErrorMessage = "unavailable"
				service = &scriptedModel{scripts: []*streamScript{
					{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: failure}}}, success,
				}}
			} else {
				service = &startFailureModel{startErr: failure, script: success}
			}
			input := testInput(info, mustPrompt(t, "work"))
			var recorded []llm.AgentMessage
			input.MessageRecorder = captureMessages(&recorded)
			result, err := mustRetryLoop(t, service, 1, 0).Run(t.Context(), input, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertRecordedMessages(t, recorded, result.Messages())
			if len(result.ModelRounds) != 2 {
				t.Fatalf("rounds = %d", len(result.ModelRounds))
			}
		})
	}
}

func TestMessageRecorderGetsDefensiveCopies(t *testing.T) {
	t.Parallel()
	info := testModel()
	assistant := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("one", "write", `{"path":"original"}`))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(assistant)},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}}
	tool := newFakeTool("write", nil)
	input := testInput(info, mustPrompt(t, "original"))
	input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
		switch value := message.(type) {
		case llm.UserMessage:
			value.Content[0].Text = "changed"
		case llm.AssistantMessage:
			for i := range value.Content {
				if value.Content[i].ToolCall != nil {
					value.Content[i].ToolCall.Name = "changed"
					value.Content[i].ToolCall.Arguments[0] = '!'
				}
			}
		case llm.ToolResultMessage:
			value.Content[0].Text = "changed"
		}
		return nil
	}
	result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Prompt.Content[0].Text != "original" || len(tool.calls) != 1 || string(tool.calls[0].Arguments) != `{"path":"original"}` {
		t.Fatalf("callback mutated result or executed call: %#v %#v", result, tool.calls)
	}
	if result.ModelRounds[0].ToolResults[0].Content[0].Text == "changed" {
		t.Fatal("tool result alias")
	}
}

func TestMessageRecorderCancellationRetainsOriginalContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	info := testModel()
	assistant := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("one", "write", `{}`), toolCallPart("two", "write", `{}`))
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(assistant)}}}
	tool := newFakeTool("write", func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		cancel()
		return llm.ToolResult{Content: []llm.ContentPart{textPart("completed")}}, nil
	})
	input := testInput(info, mustPrompt(t, "work"))
	var recorded []llm.AgentMessage
	canceledRecords := 0
	input.MessageRecorder = func(recordCtx context.Context, message llm.AgentMessage) error {
		if recordCtx != ctx {
			t.Fatal("recorder context replaced")
		}
		if errors.Is(recordCtx.Err(), context.Canceled) {
			canceledRecords++
		}
		recorded = append(recorded, message)
		return nil
	}
	result, err := mustLoop(t, model, []agent.Tool{tool}).Run(ctx, input, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	assertRecordedMessages(t, recorded, result.Messages())
	if canceledRecords != 3 || len(tool.calls) != 1 {
		t.Fatalf("canceled records/tools = %d/%d", canceledRecords, len(tool.calls))
	}
}

func captureMessages(messages *[]llm.AgentMessage) agent.MessageRecorder {
	return func(_ context.Context, message llm.AgentMessage) error {
		*messages = append(*messages, message)
		return nil
	}
}

func assertRecordedMessages(t *testing.T, got, want []llm.AgentMessage) {
	t.Helper()
	gotJSON, err := llm.MarshalAgentMessages(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := llm.MarshalAgentMessages(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("recorded = %s\nwant = %s", gotJSON, wantJSON)
	}
}

func TestMessageRecorderFailureDuringRetryPreventsRetry(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := &startFailureModel{
		startErr: &llm.ProviderError{StatusCode: 503, Err: errors.New("unavailable")},
		script:   &streamScript{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}
	input := testInput(info, mustPrompt(t, "work"))
	failure := errors.New("disk full")
	calls := 0
	input.MessageRecorder = func(context.Context, llm.AgentMessage) error {
		calls++
		if calls == 2 {
			return failure
		}
		return nil
	}
	_, err := mustRetryLoop(t, model, 2, 0).Run(t.Context(), input, nil)
	if !errors.Is(err, failure) || calls != 2 || model.calls != 1 {
		t.Fatalf("error/records/requests = %v/%d/%d", err, calls, model.calls)
	}
}

func TestMessageRecorderFailureDuringFinalCleanupIsNotRetried(t *testing.T) {
	t.Parallel()
	info := testModel()
	assistant := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("one", "write", `{}`), toolCallPart("two", "write", `{}`))
	model := &scriptedModel{scripts: []*streamScript{{events: terminalEvents(assistant)}}}
	tool := newFakeTool("write", nil)
	input := testInput(info, mustPrompt(t, "work"))
	diskErr, displayErr := errors.New("disk full"), errors.New("display closed")
	calls := 0
	input.MessageRecorder = func(context.Context, llm.AgentMessage) error {
		calls++
		if calls == 4 {
			return diskErr
		}
		return nil
	}
	result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input,
		func(_ context.Context, event agent.AgentEvent) error {
			if event.Type == agent.EventTypeToolExecutionEnd {
				return displayErr
			}
			return nil
		})
	if !errors.Is(err, diskErr) || !errors.Is(err, displayErr) || calls != 4 || len(tool.calls) != 1 {
		t.Fatalf("error/records/tools = %v/%d/%d", err, calls, len(tool.calls))
	}
	if len(result.ModelRounds[0].ToolResults) != 2 {
		t.Fatal("result lost synthetic pairing")
	}
}

func TestMessageRecorderPreservesAcceptedInputWhenPreparationFails(t *testing.T) {
	t.Parallel()
	info := testModel()
	info.ContextWindow = 1
	model := &scriptedModel{}
	input := testInput(info, mustPrompt(t, "work"))
	var recorded []llm.AgentMessage
	input.MessageRecorder = captureMessages(&recorded)
	result, err := mustLoop(t, model, nil).Run(t.Context(), input, nil)
	if !errors.Is(err, agent.ErrContextLimit) {
		t.Fatalf("error = %v", err)
	}
	assertRecordedMessages(t, recorded, result.Messages())
	if len(recorded) != 2 || len(model.requests) != 0 {
		t.Fatalf("records/requests = %d/%d", len(recorded), len(model.requests))
	}
}

func TestMessageRecorderTruncatedToolCallsAreRecordedWithoutExecution(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(assistantMessage(info, llm.StopReasonLength, toolCallPart("one", "write", `{}`)))},
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}}
	tool := newFakeTool("write", nil)
	input := testInput(info, mustPrompt(t, "work"))
	var recorded []llm.AgentMessage
	input.MessageRecorder = captureMessages(&recorded)
	result, err := mustLoop(t, model, []agent.Tool{tool}).Run(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertRecordedMessages(t, recorded, result.Messages())
	if len(tool.calls) != 0 || !result.ModelRounds[0].ToolResults[0].IsError {
		t.Fatal("truncated call executed")
	}
}

func TestMessageRecorderSyntheticFailurePreservesProviderCause(t *testing.T) {
	t.Parallel()
	info := testModel()
	providerErr := &llm.ProviderError{StatusCode: 503, Err: errors.New("provider unavailable")}
	diskErr := errors.New("disk full")
	failed := assistantMessage(info, llm.StopReasonError, toolCallPart("one", "write", `{}`))
	failed.ErrorMessage = providerErr.Error()
	model := &scriptedModel{scripts: []*streamScript{{events: []llm.Event{
		{Type: llm.EventTypeStart},
		{Type: llm.EventTypeError, Message: &failed, Err: providerErr},
	}}}}
	tool := newFakeTool("write", nil)
	input := testInput(info, mustPrompt(t, "work"))
	calls := 0
	input.MessageRecorder = func(_ context.Context, message llm.AgentMessage) error {
		calls++
		if _, ok := message.(llm.ToolResultMessage); ok {
			return diskErr
		}
		return nil
	}
	_, err := mustLoop(t, model, []agent.Tool{tool}, agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: 2})).Run(t.Context(), input, nil)
	if !errors.Is(err, providerErr) || !errors.Is(err, diskErr) {
		t.Fatalf("combined error = %v", err)
	}
	if calls != 3 || len(model.requests) != 1 || len(tool.calls) != 0 {
		t.Fatalf("records/requests/tools = %d/%d/%d", calls, len(model.requests), len(tool.calls))
	}
}
