package agent

import (
	"context"
	"time"
)

// eventLifecycle is the Loop's position in its event stream: the current
// turn number, the retry attempt of the current model round (0 for the first
// try) and whether that attempt's assistant message has started. Turn and
// retry boundaries are emitted only through the methods below, so numbering
// and pairing are not threaded through every phase.
type eventLifecycle struct {
	turn             int
	attempt          int
	assistantStarted bool
}

func (e *runExecution) beginTurn(ctx context.Context) error {
	e.lifecycle.turn++
	e.lifecycle.assistantStarted = false
	return e.emit(ctx, AgentEvent{Type: EventTypeTurnStart, TurnNumber: e.lifecycle.turn})
}

// endTurn closes the current turn; event carries its message, results and error.
func (e *runExecution) endTurn(ctx context.Context, event AgentEvent) error {
	event.Type, event.TurnNumber = EventTypeTurnEnd, e.lifecycle.turn
	return e.emit(ctx, event)
}

func (e *runExecution) beginRetry(ctx context.Context, delay time.Duration, runErr error) error {
	e.lifecycle.attempt++
	return e.emit(ctx, AgentEvent{
		Type: EventTypeRetryStart,
		Retry: &RetryEvent{
			Attempt:    e.lifecycle.attempt,
			MaxRetries: e.loop.retry.MaxRetries,
			Delay:      delay,
			Err:        runErr,
		},
	})
}

// endRetry closes an open retry sequence with its last attempt's outcome; it
// does nothing when the current model round was not retried.
func (e *runExecution) endRetry(ctx context.Context, success bool, runErr error) error {
	attempt := e.lifecycle.attempt
	if attempt == 0 {
		return nil
	}
	e.lifecycle.attempt = 0
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
