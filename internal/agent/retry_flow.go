package agent

import (
	"context"
	"errors"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// recordIncompleteAttempt records a model attempt that never completed and
// closes its message and turn.
func (e *runExecution) recordIncompleteAttempt(ctx context.Context, runErr error) error {
	message := llm.NewAssistantMessage(e.input.Model)
	message.StopReason = llm.StopReasonError
	message.ErrorMessage = withProviderSummary("model request failed before completion", runErr)
	message.RequestConfig = e.requestConfigID
	return e.closeFailedAttempt(ctx, message, runErr)
}

// recordTerminalAttempt closes a started attempt that ends the run with the
// terminal failure finalization would otherwise record, so the assistant
// message that began streaming is paired.
func (e *runExecution) recordTerminalAttempt(ctx context.Context, runErr error) error {
	message, err := terminalMessage(e.input.Model, withTimeBudget(ctx, runErr))
	if err != nil {
		return err
	}
	return e.closeFailedAttempt(ctx, message, runErr)
}

// closeFailedAttempt records message as the current turn's assistant reply
// and closes its message and turn.
func (e *runExecution) closeFailedAttempt(ctx context.Context, message llm.AssistantMessage, runErr error) error {
	turnNumber := e.lifecycle.turn
	turn := ModelRound{
		Number:      turnNumber,
		Inputs:      e.takePendingInputs(),
		Assistant:   message,
		ToolResults: []llm.ToolResultMessage{},
	}
	e.result.ModelRounds = append(e.result.ModelRounds, turn)
	if err := e.recordMessage(ctx, message); err != nil {
		return err
	}

	if !e.lifecycle.assistantStarted {
		if err := e.emit(ctx, AgentEvent{
			Type:       EventTypeMessageStart,
			TurnNumber: turnNumber,
			Message:    message,
		}); err != nil {
			return err
		}
	}
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeMessageEnd,
		TurnNumber: turnNumber,
		Message:    message,
		Err:        runErr,
	}); err != nil {
		return err
	}
	return e.endTurn(ctx, AgentEvent{Message: message, Err: runErr})
}

// retryTurn settles a failed model turn. runErr is the error that failed the
// turn. record, when non-nil, marks a turn still open after an incomplete
// stream: it records the failed attempt before a retry or after the last
// retry. A first attempt that is not retried records the terminal failure
// when its message started and otherwise only closes the turn. A final
// retry_end follows the last attempt's turn_end. force
// retries irrespective of the retry policy with no backoff; the loop uses it
// exactly once after arming reasoning degradation, when the retried request
// differs from the rejected one. stepContinue means a retry turn has started.
func (e *runExecution) retryTurn(
	ctx context.Context,
	runErr error,
	record func() error,
	force bool,
) (step, error) {
	delay, retry := e.loop.retry.decision(runErr, e.lifecycle.attempt+1)
	if force {
		retry = true
		delay = 0
	}
	if !retry {
		// After retries the run ends at an earlier attempt's record, so
		// finalization adds no terminal message; record this attempt too.
		// Without retries, finalization records the terminal failure unless
		// the attempt's message already started.
		var closeErr error
		switch {
		case record == nil:
		case e.lifecycle.attempt > 0:
			closeErr = record()
		case e.lifecycle.assistantStarted:
			closeErr = e.recordTerminalAttempt(ctx, runErr)
		default:
			closeErr = e.endTurn(ctx, AgentEvent{Err: runErr})
		}
		if closeErr != nil {
			return stepAbort, errors.Join(runErr, closeErr)
		}
		if err := e.endRetry(ctx, false, runErr); err != nil {
			return stepAbort, errors.Join(runErr, err)
		}
		return stepEnd, runErr
	}
	if record != nil {
		if err := record(); err != nil {
			return stepAbort, errors.Join(runErr, err)
		}
	}
	if err := e.beginRetry(ctx, delay, runErr); err != nil {
		return stepAbort, errors.Join(runErr, err)
	}
	if err := waitRetry(ctx, delay); err != nil {
		waitErr := errors.Join(runErr, err)
		_ = e.endRetry(ctx, false, waitErr)
		return stepEnd, waitErr
	}
	if err := e.beginTurn(ctx); err != nil {
		return stepAbort, err
	}
	return stepContinue, nil
}

// withProviderSummary appends the provider failure metadata to text so a
// Session distinguishes an upstream outage from a request AICE built wrongly.
func withProviderSummary(text string, runErr error) string {
	var providerErr *llm.ProviderError
	if !errors.As(runErr, &providerErr) {
		return text
	}
	return text + ": " + providerErr.Summary()
}
