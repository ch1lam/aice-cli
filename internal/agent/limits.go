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
}

// WithRunLimits configures immutable limits; counters belong to each Run.
func WithRunLimits(limits RunLimits) LoopOption {
	return func(loop *Loop) error {
		if limits.Tokens < 0 || limits.Timeout < 0 || limits.NoProgress < 0 || limits.MaxTurns < 0 {
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
