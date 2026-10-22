package agent

import (
	"context"
	"errors"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func (e *runExecution) recordIncompleteAttempt(
	ctx context.Context,
	turnNumber int,
	messageStarted bool,
	runErr error,
) error {
	message := llm.NewAssistantMessage(e.input.Model)
	message.StopReason = llm.StopReasonError
	message.ErrorMessage = "model request failed before completion"
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

	if !messageStarted {
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

	return e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnEnd,
		TurnNumber: turnNumber,
		Message:    message,
		Err:        runErr,
	})
}

func (e *runExecution) startRetry(
	ctx context.Context,
	attempt int,
	delay time.Duration,
	runErr error,
) error {
	return e.emit(ctx, AgentEvent{
		Type: EventTypeRetryStart,
		Retry: &RetryEvent{
			Attempt:    attempt,
			MaxRetries: e.loop.retry.MaxRetries,
			Delay:      delay,
			Err:        runErr,
		},
	})
}

func (e *runExecution) emitRetryEnd(
	ctx context.Context,
	attempt int,
	success bool,
	runErr error,
) error {
	return e.emit(ctx, AgentEvent{
		Type: EventTypeRetryEnd,
		Retry: &RetryEvent{
			Attempt:    attempt,
			MaxRetries: e.loop.retry.MaxRetries,
			Success:    success,
			Err:        runErr,
		},
	})
}

// retryTurn settles a failed model turn. runErr is the error that failed the
// turn. record, when non-nil, marks a turn still open after an incomplete
// stream: it records the failed attempt before a retry, and on exhaustion the
// turn is closed with turn_end. force retries irrespective of the retry policy
// with no backoff; the loop uses it exactly once after arming reasoning
// degradation, when the retried request differs from the rejected one.
// stepContinue means attempt and turn advanced to the retry.
func (e *runExecution) retryTurn(
	ctx context.Context,
	attempt *int,
	turn *int,
	runErr error,
	record func() error,
	force bool,
) (step, error) {
	nextAttempt := *attempt + 1
	delay, retry := e.loop.retry.decision(runErr, nextAttempt)
	if force {
		retry = true
		delay = 0
	}
	if !retry {
		if *attempt > 0 {
			if err := e.emitRetryEnd(ctx, *attempt, false, runErr); err != nil {
				return stepAbort, errors.Join(runErr, err)
			}
		}
		if record != nil {
			if err := e.emit(ctx, AgentEvent{
				Type:       EventTypeTurnEnd,
				TurnNumber: *turn,
				Err:        runErr,
			}); err != nil {
				return stepAbort, errors.Join(runErr, err)
			}
		}
		return stepEnd, runErr
	}
	if record != nil {
		if err := record(); err != nil {
			return stepAbort, errors.Join(runErr, err)
		}
	}
	if err := e.startRetry(ctx, nextAttempt, delay, runErr); err != nil {
		return stepAbort, errors.Join(runErr, err)
	}
	if err := waitRetry(ctx, delay); err != nil {
		waitErr := errors.Join(runErr, err)
		_ = e.emitRetryEnd(ctx, nextAttempt, false, waitErr)
		return stepEnd, waitErr
	}
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnStart,
		TurnNumber: *turn + 1,
	}); err != nil {
		return stepAbort, err
	}
	*attempt = nextAttempt
	(*turn)++
	return stepContinue, nil
}
