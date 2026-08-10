package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func (e *runExecution) finishRun(ctx context.Context, runErr error) (Result, error) {
	result, finalizeErr := finalizeFailedResult(
		e.result,
		e.input.Model,
		runErr,
		e.takePendingSteering(),
	)
	e.result = result
	if finalizeErr != nil {
		runErr = errors.Join(runErr, finalizeErr)
	}
	if err := e.emit(ctx, AgentEvent{
		Type:     EventTypeAgentEnd,
		Messages: result.Messages(),
		Err:      runErr,
	}); err != nil {
		return e.result, errors.Join(runErr, err)
	}
	return e.result, runErr
}

func (e *runExecution) finishIncompleteTurn(
	ctx context.Context,
	turnNumber int,
	runErr error,
) (Result, error) {
	if runErr == nil {
		runErr = fmt.Errorf("%w: assistant turn did not complete", ErrProtocol)
	}
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnEnd,
		TurnNumber: turnNumber,
		Err:        runErr,
	}); err != nil {
		return e.result, errors.Join(runErr, err)
	}
	return e.finishRun(ctx, runErr)
}

func finalizeRunResult(
	result Result,
	model llm.Model,
	runErr error,
	pendingSteering []llm.UserMessage,
) (Result, error) {
	result, finalizeErr := finalizeFailedResult(
		result,
		model,
		runErr,
		pendingSteering,
	)
	if finalizeErr != nil {
		runErr = errors.Join(runErr, finalizeErr)
	}
	return result, runErr
}

func finalizeFailedResult(
	result Result,
	model llm.Model,
	runErr error,
	pendingSteering []llm.UserMessage,
) (Result, error) {
	if runErr == nil {
		return result, nil
	}

	terminalText, stopReason := terminalFailure(runErr)
	paired, err := pairUnfinishedToolCalls(result, terminalText)
	if err != nil {
		return result, fmt.Errorf("agent: pair unfinished tool calls: %w", err)
	}
	result = paired
	if len(pendingSteering) == 0 &&
		resultEndsAtAssistant(result) &&
		!needsAbortedTerminal(result, stopReason) {
		return result, nil
	}
	if err := result.Prompt.Validate(); err != nil {
		return result, fmt.Errorf("agent: validate failed prompt: %w", err)
	}

	message := llm.NewAssistantMessage(model)
	message.Content = []llm.ContentPart{
		llm.NewTextContent(terminalText).Part(),
	}
	message.StopReason = stopReason
	message.ErrorMessage = terminalText
	if err := message.Validate(); err != nil {
		return result, fmt.Errorf("agent: construct terminal failure: %w", err)
	}

	turnNumber := 1
	if len(result.Turns) > 0 {
		turnNumber = result.Turns[len(result.Turns)-1].Number + 1
	}
	result.Turns = append(result.Turns, Turn{
		Number:      turnNumber,
		Steering:    pendingSteering,
		Assistant:   message,
		ToolResults: []llm.ToolResultMessage{},
	})
	return result, nil
}

func needsAbortedTerminal(result Result, stopReason llm.StopReason) bool {
	if stopReason != llm.StopReasonAborted || len(result.Turns) == 0 {
		return false
	}
	return result.Turns[len(result.Turns)-1].Assistant.StopReason != llm.StopReasonAborted
}

func pairUnfinishedToolCalls(result Result, terminalText string) (Result, error) {
	for turnIndex := range result.Turns {
		turn := &result.Turns[turnIndex]
		calls, err := extractToolCalls(turn.Assistant)
		if err != nil {
			return result, err
		}

		recorded := make(map[string]struct{}, len(turn.ToolResults))
		for _, toolResult := range turn.ToolResults {
			recorded[toolResult.ToolCallID] = struct{}{}
		}
		for _, call := range calls {
			if _, exists := recorded[call.ID]; exists {
				continue
			}
			message, err := newErrorToolResult(
				call,
				fmt.Errorf(
					"tool %q was not executed because the %s",
					call.Name,
					terminalText,
				),
			)
			if err != nil {
				return result, err
			}
			turn.ToolResults = append(turn.ToolResults, message)
			recorded[call.ID] = struct{}{}
		}
	}
	return result, nil
}

func resultEndsAtAssistant(result Result) bool {
	if len(result.Turns) == 0 {
		return false
	}
	last := result.Turns[len(result.Turns)-1]
	if len(last.ToolResults) != 0 {
		return false
	}
	for _, part := range last.Assistant.Content {
		if part.Type == llm.ContentTypeToolCall {
			return false
		}
	}
	return true
}

func terminalFailure(runErr error) (string, llm.StopReason) {
	switch {
	case errors.Is(runErr, context.Canceled):
		return "agent run canceled before completion", llm.StopReasonAborted
	case errors.Is(runErr, context.DeadlineExceeded):
		return "agent run deadline exceeded before completion", llm.StopReasonAborted
	case errors.Is(runErr, ErrContextLimit):
		return "agent run stopped after reaching the context limit", llm.StopReasonError
	default:
		return "agent run failed before completion", llm.StopReasonError
	}
}
