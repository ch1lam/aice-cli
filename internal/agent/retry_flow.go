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

	turn := Turn{
		Number:      turnNumber,
		Steering:    e.takePendingSteering(),
		Assistant:   message,
		ToolResults: []llm.ToolResultMessage{},
	}
	e.result.Turns = append(e.result.Turns, turn)
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

// retryOutcome describes the result of one retry decision. retry is false
// when retries are exhausted or the retry flow was interrupted; stopErr stops
// the run immediately without an agent_end event; waitErr ends the run because
// the retry wait was interrupted.
type retryOutcome struct {
	nextAttempt int
	nextTurn    int
	retry       bool
	stopErr     error
	waitErr     error
}

// retryTurn advances one retry attempt after a failed model turn. runErr is
// the error that failed the turn; record, when non-nil, records the failed
// attempt before the retry.
func (e *runExecution) retryTurn(
	ctx context.Context,
	retryAttempt int,
	turnNumber int,
	runErr error,
	record func() error,
) retryOutcome {
	nextAttempt := retryAttempt + 1
	delay, retry := e.loop.retry.decision(runErr, nextAttempt)
	if !retry {
		if retryAttempt > 0 {
			if err := e.emitRetryEnd(ctx, retryAttempt, false, runErr); err != nil {
				return retryOutcome{stopErr: errors.Join(runErr, err)}
			}
		}
		return retryOutcome{}
	}
	if record != nil {
		if err := record(); err != nil {
			return retryOutcome{stopErr: errors.Join(runErr, err)}
		}
	}
	if err := e.startRetry(ctx, nextAttempt, delay, runErr); err != nil {
		return retryOutcome{stopErr: errors.Join(runErr, err)}
	}
	if err := waitRetry(ctx, delay); err != nil {
		waitErr := errors.Join(runErr, err)
		_ = e.emitRetryEnd(ctx, nextAttempt, false, waitErr)
		return retryOutcome{waitErr: waitErr}
	}
	nextTurn := turnNumber + 1
	if err := e.emit(ctx, AgentEvent{
		Type:       EventTypeTurnStart,
		TurnNumber: nextTurn,
	}); err != nil {
		return retryOutcome{stopErr: err}
	}
	return retryOutcome{nextAttempt: nextAttempt, nextTurn: nextTurn, retry: true}
}
