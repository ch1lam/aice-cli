package llm

import (
	"fmt"
	"math"
)

const tokensPerMillion = 1_000_000

// Pricing contains model rates in US dollars per million tokens.
type Pricing struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// EstimateCost applies model pricing to provider-reported token usage. A nil
// result means the model has no per-token pricing metadata.
func EstimateCost(pricing Pricing, usage Usage) *Cost {
	if pricing == (Pricing{}) {
		return nil
	}

	cost := &Cost{
		Input:      tokenCost(usage.InputTokens, pricing.Input),
		Output:     tokenCost(usage.OutputTokens, pricing.Output),
		CacheRead:  tokenCost(usage.CacheReadTokens, pricing.CacheRead),
		CacheWrite: tokenCost(usage.CacheWriteTokens, pricing.CacheWrite),
	}
	cost.Total = cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite
	return cost
}

// AddUsage returns the sum of two normalized model usages without retaining
// either input's Cost pointer.
func AddUsage(left, right Usage) Usage {
	total := Usage{
		InputTokens:      left.InputTokens + right.InputTokens,
		OutputTokens:     left.OutputTokens + right.OutputTokens,
		ReasoningTokens:  left.ReasoningTokens + right.ReasoningTokens,
		CacheReadTokens:  left.CacheReadTokens + right.CacheReadTokens,
		CacheWriteTokens: left.CacheWriteTokens + right.CacheWriteTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
	}
	if left.Cost == nil && right.Cost == nil {
		return total
	}

	total.Cost = &Cost{}
	addCost(total.Cost, left.Cost)
	addCost(total.Cost, right.Cost)
	return total
}

func (p Pricing) validate() error {
	rates := []struct {
		name string
		rate float64
	}{
		{name: "input", rate: p.Input},
		{name: "output", rate: p.Output},
		{name: "cache read", rate: p.CacheRead},
		{name: "cache write", rate: p.CacheWrite},
	}
	for _, item := range rates {
		if math.IsNaN(item.rate) || math.IsInf(item.rate, 0) {
			return fmt.Errorf(
				"llm: request model %s price must be finite",
				item.name,
			)
		}
		if item.rate < 0 {
			return fmt.Errorf(
				"llm: request model %s price cannot be negative",
				item.name,
			)
		}
	}
	return nil
}

func tokenCost(tokens int64, rate float64) float64 {
	return float64(tokens) * rate / tokensPerMillion
}

func addCost(total *Cost, cost *Cost) {
	if cost == nil {
		return
	}
	total.Input += cost.Input
	total.Output += cost.Output
	total.CacheRead += cost.CacheRead
	total.CacheWrite += cost.CacheWrite
	total.Total += cost.Total
}
