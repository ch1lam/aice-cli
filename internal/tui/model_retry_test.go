package tui

import (
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
)

func TestModelShowsRetryLifecycleStatus(t *testing.T) {
	t.Parallel()

	current := model{}
	changed, command := current.applyAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeRetryStart,
		Retry: &agent.RetryEvent{
			Attempt:    2,
			MaxRetries: 3,
			Delay:      4 * time.Second,
		},
	})
	if !changed || command != nil || current.status != "Retrying in 4s (2/3)..." {
		t.Fatalf("retry_start state = %q, %v, %#v", current.status, changed, command)
	}

	changed, command = current.applyAgentEvent(agent.AgentEvent{
		Type:  agent.EventTypeRetryEnd,
		Retry: &agent.RetryEvent{Attempt: 2, MaxRetries: 3, Success: true},
	})
	if !changed || command != nil || current.status != "Retry succeeded" {
		t.Fatalf("retry_end state = %q, %v, %#v", current.status, changed, command)
	}
}
