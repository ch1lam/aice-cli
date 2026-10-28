package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

var (
	ErrMaxTurns    = errors.New("agent: maximum model turns reached")
	ErrTokenBudget = errors.New("agent: run token budget exhausted")
	ErrTimeBudget  = errors.New("agent: run time budget exhausted")
	// ErrGuardDenials stops a run whose tool calls keep being denied.
	ErrGuardDenials = errors.New("agent: too many denied tool calls")
)

// RunLimits applies independently to each Run, including its queued inputs.
// Zero values leave resource use unlimited. Tokens are provider-reported usage,
// checked between operations, not an exact billing or output-token ceiling.
type RunLimits struct {
	// MaxTurns bounds model request attempts, including retries. Zero is unlimited.
	MaxTurns int
	Tokens   int64
	Timeout  time.Duration
	// NoProgress stops consecutive identical tool rounds. Zero disables it.
	NoProgress int
	// GuardDenials stops the run after this many denied tool calls in a row
	// or in total. Zero fields are unlimited.
	GuardDenials GuardDenialLimit
}

// GuardDenialLimit bounds tool calls the Guard denied or a person declined.
type GuardDenialLimit struct {
	Consecutive int
	Total       int
}

// WithRunLimits configures immutable limits; counters belong to each Run.
func WithRunLimits(limits RunLimits) LoopOption {
	return func(loop *Loop) error {
		if limits.Tokens < 0 || limits.Timeout < 0 || limits.NoProgress < 0 || limits.MaxTurns < 0 ||
			limits.GuardDenials.Consecutive < 0 || limits.GuardDenials.Total < 0 {
			return errors.New("agent: run limits cannot be negative")
		}
		if limits.NoProgress == 1 {
			return errors.New("agent: no-progress limit must be zero or at least two")
		}
		loop.limits = limits
		return nil
	}
}

func (e *runExecution) checkBudget(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	if limit := e.loop.limits.Tokens; limit > 0 && e.tokensUsed >= limit {
		return fmt.Errorf("%w (%d of %d tokens); send a new message to continue with a fresh run budget", ErrTokenBudget, e.tokensUsed, limit)
	}
	return nil
}

func (e *runExecution) addUsage(usage llm.Usage) {
	e.result.Usage = llm.AddUsage(e.result.Usage, usage)
	tokens := usage.TotalTokens
	if tokens <= 0 {
		// Normalized input excludes cache reads/writes; reasoning is included
		// in output and must not be charged twice.
		tokens = 0
		for _, n := range []int64{usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens} {
			tokens += min(max(n, 0), math.MaxInt64-tokens)
		}
	}
	e.tokensUsed += min(max(tokens, 0), math.MaxInt64-e.tokensUsed)
}

// checkMaxTurns applies only before model requests, allowing the last permitted
// response's complete tool batch to settle through the ordinary Guard path.
func (e *runExecution) checkMaxTurns() error {
	if limit := e.loop.limits.MaxTurns; limit > 0 && e.turnsUsed >= limit {
		return fmt.Errorf("%w (%d); send a new message to continue with a fresh turn budget", ErrMaxTurns, limit)
	}
	return nil
}

// noteGuardDecision counts calls that never reached their tool because the
// Guard denied them or a person declined; any dispatched call resets the run.
func (e *runExecution) noteGuardDecision(denied bool) {
	if !denied {
		e.guardDenialsInRow = 0
		return
	}
	e.guardDenialsInRow++
	e.guardDenialsTotal++
}

// checkGuardDenials applies after a complete tool round.
func (e *runExecution) checkGuardDenials() error {
	limit := e.loop.limits.GuardDenials
	switch {
	case limit.Consecutive > 0 && e.guardDenialsInRow >= limit.Consecutive:
		return fmt.Errorf("%w (%d in a row); review the denials before continuing", ErrGuardDenials, e.guardDenialsInRow)
	case limit.Total > 0 && e.guardDenialsTotal >= limit.Total:
		return fmt.Errorf("%w (%d in this run); review the denials before continuing", ErrGuardDenials, e.guardDenialsTotal)
	}
	return nil
}
