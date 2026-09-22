package agent

import (
	"errors"
	"fmt"
	"testing"
)

func TestReasoningReplayRejected(t *testing.T) {
	t.Parallel()
	rejected := errors.New("upstream request failed: 400 invalid_request_error: " +
		"reasoning `encrypted_content` was not issued to this caller")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"reasoning not issued", rejected, true},
		{"wrapped reasoning not issued", fmt.Errorf(
			"agent: start model stream: openai responses: start response stream: %w",
			rejected,
		), true},
		{"unrelated provider error", errors.New(
			"openai responses: start response stream: 429 rate limit exceeded",
		), false},
		{"encrypted token without rejection", errors.New(
			"replaying encrypted_content after rate limit",
		), false},
		{"rejection without encrypted token", errors.New(
			"reasoning summary was not issued to this caller",
		), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasoningReplayRejected(tc.err); got != tc.want {
				t.Fatalf("reasoningReplayRejected(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestArmReasoningReplayRetry(t *testing.T) {
	t.Parallel()
	rejected := errors.New("reasoning `encrypted_content` was not issued to this caller")
	unrelated := errors.New("model stream failed: upstream unavailable")

	e := &runExecution{}
	if e.armReasoningReplayRetry(unrelated) {
		t.Fatal("armed degradation on an unrelated error")
	}
	if e.degradeReasoning {
		t.Fatal("degradation armed without a reasoning rejection")
	}
	if !e.armReasoningReplayRetry(rejected) {
		t.Fatal("first rejection did not arm degradation")
	}
	if !e.degradeReasoning {
		t.Fatal("rejection did not set the degradation flag")
	}
	if e.armReasoningReplayRetry(rejected) {
		t.Fatal("second rejection re-armed a one-shot degradation")
	}
}
