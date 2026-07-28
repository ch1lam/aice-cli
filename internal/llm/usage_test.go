package llm_test

import (
	"math"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestEstimateCostUsesPerMillionTokenRates(t *testing.T) {
	t.Parallel()

	got := llm.EstimateCost(
		llm.Pricing{
			Input:     0.14,
			Output:    0.28,
			CacheRead: 0.0028,
		},
		llm.Usage{
			InputTokens:      1_000_000,
			OutputTokens:     500_000,
			CacheReadTokens:  250_000,
			CacheWriteTokens: 125_000,
		},
	)
	if got == nil {
		t.Fatal("EstimateCost() = nil, want estimated cost")
	}
	want := llm.Cost{
		Input:     0.14,
		Output:    0.14,
		CacheRead: 0.0007,
		Total:     0.2807,
	}
	if !costsClose(*got, want) {
		t.Errorf("EstimateCost() = %#v, want %#v", *got, want)
	}
}

func TestEstimateCostWithoutPricingIsUnknown(t *testing.T) {
	t.Parallel()

	if got := llm.EstimateCost(llm.Pricing{}, llm.Usage{
		InputTokens: 1_000,
	}); got != nil {
		t.Errorf("EstimateCost() = %#v, want nil without pricing", got)
	}
}

func TestAddUsageSumsTokensAndCosts(t *testing.T) {
	t.Parallel()

	leftCost := &llm.Cost{Input: 0.01, Total: 0.01}
	left := llm.Usage{
		InputTokens:     100,
		OutputTokens:    20,
		ReasoningTokens: 5,
		TotalTokens:     120,
		Cost:            leftCost,
	}
	right := llm.Usage{
		InputTokens:     50,
		OutputTokens:    30,
		CacheReadTokens: 40,
		TotalTokens:     120,
		Cost:            &llm.Cost{Output: 0.02, CacheRead: 0.001, Total: 0.021},
	}

	got := llm.AddUsage(left, right)

	want := llm.Usage{
		InputTokens:      150,
		OutputTokens:     50,
		ReasoningTokens:  5,
		CacheReadTokens:  40,
		CacheWriteTokens: 0,
		TotalTokens:      240,
		Cost: &llm.Cost{
			Input:     0.01,
			Output:    0.02,
			CacheRead: 0.001,
			Total:     0.031,
		},
	}
	if got.InputTokens != want.InputTokens ||
		got.OutputTokens != want.OutputTokens ||
		got.ReasoningTokens != want.ReasoningTokens ||
		got.CacheReadTokens != want.CacheReadTokens ||
		got.CacheWriteTokens != want.CacheWriteTokens ||
		got.TotalTokens != want.TotalTokens ||
		got.Cost == nil ||
		!costsClose(*got.Cost, *want.Cost) {
		t.Fatalf("AddUsage() = %#v, want %#v", got, want)
	}

	leftCost.Total = 99
	if got.Cost.Total != want.Cost.Total {
		t.Errorf("AddUsage() retained an input cost pointer: %#v", got.Cost)
	}
}

func TestRequestValidateRejectsInvalidPricing(t *testing.T) {
	t.Parallel()

	request := llm.Request{
		Model: llm.Model{
			ID:        "test-model",
			API:       "test-api",
			Provider:  "test-provider",
			MaxTokens: 1,
			Pricing:   llm.Pricing{Input: math.Inf(1)},
		},
		Messages: []llm.Message{llm.UserMessage{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{llm.NewTextContent("hello").Part()},
		}},
	}

	err := request.Validate()
	if err == nil || !strings.Contains(err.Error(), "input price must be finite") {
		t.Fatalf("Validate() error = %v, want invalid pricing error", err)
	}
}

func costsClose(left, right llm.Cost) bool {
	const tolerance = 1e-12
	return math.Abs(left.Input-right.Input) < tolerance &&
		math.Abs(left.Output-right.Output) < tolerance &&
		math.Abs(left.CacheRead-right.CacheRead) < tolerance &&
		math.Abs(left.CacheWrite-right.CacheWrite) < tolerance &&
		math.Abs(left.Total-right.Total) < tolerance
}
