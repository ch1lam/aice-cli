package tui

import (
	"github.com/ch1lam/aice-cli/internal/interaction"
	"testing"
)

func TestModelShowsRetryLifecycleStatus(t *testing.T) {
	t.Parallel()

	current := model{}
	changed, command := current.applyAgentEvent(interaction.Event{
		Kind: interaction.EventRetryStart,
		Retry: interaction.RetryDisplay{
			Attempt:    2,
			MaxRetries: 3,
			Delay:      "4s",
		},
	})
	if !changed || command != nil || current.status != "Retrying in 4s (2/3)..." {
		t.Fatalf("retry_start state = %q, %v, %#v", current.status, changed, command)
	}

	changed, command = current.applyAgentEvent(interaction.Event{
		Kind:  interaction.EventRetryEnd,
		Retry: interaction.RetryDisplay{Succeeded: true},
	})
	if !changed || command != nil || current.status != "Retry succeeded" {
		t.Fatalf("retry_end state = %q, %v, %#v", current.status, changed, command)
	}
}
