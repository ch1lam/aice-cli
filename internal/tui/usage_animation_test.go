package tui

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestUsageAnimationRollsToTargetAndRejectsStaleTicks(t *testing.T) {
	t.Parallel()

	var animation usageAnimation
	target := llm.Usage{
		InputTokens:      1_200,
		OutputTokens:     456,
		CacheReadTokens:  100,
		CacheWriteTokens: 20,
		TotalTokens:      1_776,
		Cost:             &llm.Cost{Total: 0.0074},
	}
	if command := animation.Start(llm.Usage{}, target); command == nil {
		t.Fatal("Start() command = nil, want first animation tick")
	}

	generation := animation.generation
	if command := animation.Update(usageAnimationTickMsg{
		generation: generation - 1,
	}); command != nil {
		t.Fatal("stale tick scheduled another animation tick")
	}
	if got := animation.Value(llm.Usage{}); got != (usageDisplay{}) {
		t.Fatalf("stale tick changed usage to %#v", got)
	}

	previousInput := int64(0)
	for range usageAnimationFrames {
		animation.Update(usageAnimationTickMsg{generation: generation})
		current := animation.Value(llm.Usage{})
		if current.inputTokens < previousInput ||
			current.inputTokens > target.InputTokens {
			t.Fatalf(
				"animated input tokens = %d after %d, target %d",
				current.inputTokens,
				previousInput,
				target.InputTokens,
			)
		}
		previousInput = current.inputTokens
	}

	got := animation.Value(llm.Usage{})
	want := newUsageDisplay(target)
	if got != want {
		t.Fatalf("animated usage = %#v, want %#v", got, want)
	}
	if animation.running {
		t.Fatal("animation remains running at its target")
	}
}
