package tui

import (
	"github.com/ch1lam/aice-cli/internal/interaction"
	"testing"
)

func TestUsageAnimationRollsToTargetAndRejectsStaleTicks(t *testing.T) {
	t.Parallel()

	var animation usageAnimation
	target := interaction.DisplayUsage{
		InputTokens:      1_200,
		OutputTokens:     456,
		CacheReadTokens:  100,
		CacheWriteTokens: 20,
		TotalCost:        0.0074,
	}
	if command := animation.Start(interaction.DisplayUsage{}, target); command == nil {
		t.Fatal("Start() command = nil, want first animation tick")
	}

	generation := animation.generation
	if command := animation.Update(usageAnimationTickMsg{
		generation: generation - 1,
	}); command != nil {
		t.Fatal("stale tick scheduled another animation tick")
	}
	if got := animation.Value(interaction.DisplayUsage{}); got != (interaction.DisplayUsage{}) {
		t.Fatalf("stale tick changed usage to %#v", got)
	}

	previousInput := int64(0)
	for range usageAnimationFrames {
		animation.Update(usageAnimationTickMsg{generation: generation})
		current := animation.Value(interaction.DisplayUsage{})
		if current.InputTokens < previousInput ||
			current.InputTokens > target.InputTokens {
			t.Fatalf(
				"animated input tokens = %d after %d, target %d",
				current.InputTokens,
				previousInput,
				target.InputTokens,
			)
		}
		previousInput = current.InputTokens
	}

	got := animation.Value(interaction.DisplayUsage{})
	if got != target {
		t.Fatalf("animated usage = %#v, want %#v", got, target)
	}
	if animation.running {
		t.Fatal("animation remains running at its target")
	}
}
