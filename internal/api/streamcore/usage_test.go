package streamcore

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestUsageFromReport(t *testing.T) {
	t.Parallel()

	pricing := llm.Pricing{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}
	usage := UsageFromReport(pricing, 15, 4, 1, 5, 3, 99)
	if usage.InputTokens != 7 {
		t.Fatalf("InputTokens = %d, want 7 (15-5-3)", usage.InputTokens)
	}
	if usage.OutputTokens != 4 || usage.ReasoningTokens != 1 {
		t.Fatalf("output/reasoning = %d/%d", usage.OutputTokens, usage.ReasoningTokens)
	}
	if usage.CacheReadTokens != 5 || usage.CacheWriteTokens != 3 {
		t.Fatalf("cache tokens = %d/%d", usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	if usage.TotalTokens != 99 {
		t.Fatalf("TotalTokens = %d, want reported 99", usage.TotalTokens)
	}
	if usage.Cost == nil {
		t.Fatal("Cost is nil")
	}

	clamped := UsageFromReport(pricing, 2, 0, 0, 5, 0, 2)
	if clamped.InputTokens != 0 {
		t.Fatalf("clamped InputTokens = %d, want 0", clamped.InputTokens)
	}
}

func TestRecomputeTotal(t *testing.T) {
	t.Parallel()

	usage := RecomputeTotal(llm.Pricing{}, llm.Usage{
		InputTokens:      3,
		OutputTokens:     4,
		CacheReadTokens:  1,
		CacheWriteTokens: 2,
	})
	if usage.TotalTokens != 10 {
		t.Fatalf("TotalTokens = %d, want 10", usage.TotalTokens)
	}
	if usage.Cost != nil {
		t.Fatalf("Cost = %#v, want nil for empty pricing", usage.Cost)
	}
}
