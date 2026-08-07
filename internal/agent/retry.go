package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const defaultMaxRetryDelay = time.Minute

// RetryPolicy controls retries for one model call. MaxRetries excludes the
// initial request. Tool executions are never retried by this policy.
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryPolicy matches Pi's three retries with 2s, 4s, and 8s delays,
// while bounding provider-requested delays to one minute.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  2 * time.Second,
		MaxDelay:   defaultMaxRetryDelay,
	}
}

// LoopOption configures immutable agent-loop behavior.
type LoopOption func(*Loop) error

// WithRetryPolicy replaces the default model-call retry policy. A zero-value
// policy disables retries.
func WithRetryPolicy(policy RetryPolicy) LoopOption {
	return func(loop *Loop) error {
		if err := policy.validate(); err != nil {
			return err
		}
		loop.retry = policy
		return nil
	}
}

func (p RetryPolicy) validate() error {
	if p.MaxRetries < 0 {
		return errors.New("agent: retry max retries cannot be negative")
	}
	if p.BaseDelay < 0 {
		return errors.New("agent: retry base delay cannot be negative")
	}
	if p.MaxDelay < 0 {
		return errors.New("agent: retry max delay cannot be negative")
	}
	if p.MaxDelay > 0 && p.BaseDelay > p.MaxDelay {
		return errors.New("agent: retry base delay cannot exceed max delay")
	}
	return nil
}

func (p RetryPolicy) decision(err error, attempt int) (time.Duration, bool) {
	if attempt <= 0 || attempt > p.MaxRetries || !isRetryable(err) {
		return 0, false
	}

	delay := exponentialDelay(p.BaseDelay, p.MaxDelay, attempt)
	var providerErr *llm.ProviderError
	if errors.As(err, &providerErr) && providerErr.RetryAfter > delay {
		if p.MaxDelay > 0 && providerErr.RetryAfter > p.MaxDelay {
			return 0, false
		}
		delay = providerErr.RetryAfter
	}
	return delay, true
}

func exponentialDelay(base, maximum time.Duration, attempt int) time.Duration {
	delay := base
	for index := 1; index < attempt; index++ {
		if maximum > 0 && delay >= maximum {
			return maximum
		}
		if delay > time.Duration(1<<63-1)/2 {
			if maximum > 0 {
				return maximum
			}
			return time.Duration(1<<63 - 1)
		}
		delay *= 2
	}
	if maximum > 0 && delay > maximum {
		return maximum
	}
	return delay
}

func isRetryable(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrProtocol) ||
		errors.Is(err, ErrContextLimit) ||
		isEventSinkError(err) {
		return false
	}

	var providerErr *llm.ProviderError
	if errors.As(err, &providerErr) {
		return !providerErr.IsPermanent() && (providerErr.IsTransient() || providerErr.Transport)
	}

	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("agent: wait before retry: %w", ctx.Err())
	}
}
