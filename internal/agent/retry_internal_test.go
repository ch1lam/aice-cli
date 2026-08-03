package agent

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestDefaultRetryPolicyUsesThreePiStyleAttempts(t *testing.T) {
	t.Parallel()

	policy := DefaultRetryPolicy()
	err := llm.NewHTTPProviderError(
		errors.New("overloaded"),
		http.StatusServiceUnavailable,
		"",
		nil,
	)
	for attempt, want := range []time.Duration{
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	} {
		delay, retry := policy.decision(err, attempt+1)
		if !retry || delay != want {
			t.Errorf(
				"decision(attempt %d) = %s, %v, want %s, true",
				attempt+1,
				delay,
				retry,
				want,
			)
		}
	}
	if _, retry := policy.decision(err, 4); retry {
		t.Fatal("default policy retried more than three times")
	}
}

func TestRetryPolicyUsesBoundedExponentialBackoff(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxRetries: 4, BaseDelay: 2 * time.Second, MaxDelay: 5 * time.Second}
	err := llm.NewHTTPProviderError(errors.New("overloaded"), http.StatusServiceUnavailable, "", nil)
	for attempt, want := range []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		delay, retry := policy.decision(err, attempt+1)
		if !retry || delay != want {
			t.Errorf("decision(attempt %d) = %s, %v, want %s, true", attempt+1, delay, retry, want)
		}
	}
	if _, retry := policy.decision(err, 5); retry {
		t.Fatal("decision() retried beyond MaxRetries")
	}
}

func TestRetryPolicyHonorsRetryAfterWithinBound(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxRetries: 1, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	err := &llm.ProviderError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: 7 * time.Second,
		Err:        errors.New("rate limited"),
	}
	delay, retry := policy.decision(err, 1)
	if !retry || delay != 7*time.Second {
		t.Fatalf("decision() = %s, %v, want 7s, true", delay, retry)
	}

	err.RetryAfter = 11 * time.Second
	if _, retry := policy.decision(err, 1); retry {
		t.Fatal("decision() retried after provider delay exceeded MaxDelay")
	}
}

func TestRetryPolicyClassifiesProviderFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "rate limit",
			err:  &llm.ProviderError{StatusCode: 429, Err: errors.New("rate limited")},
			want: true,
		},
		{
			name: "quota is permanent",
			err: &llm.ProviderError{
				StatusCode: 429,
				Code:       "insufficient_quota",
				Err:        errors.New("quota exhausted"),
			},
		},
		{
			name: "authentication",
			err:  &llm.ProviderError{StatusCode: 401, Err: errors.New("unauthorized")},
		},
		{
			name: "transport",
			err:  llm.NewTransportProviderError(errors.New("connection reset")),
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryable(test.err); got != test.want {
				t.Fatalf("isRetryable() = %v, want %v", got, test.want)
			}
		})
	}
}
