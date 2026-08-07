package streamcore

import "github.com/ch1lam/aice-cli/internal/llm"

// AssignCost sets the estimated cost for normalized usage.
func AssignCost(pricing llm.Pricing, usage llm.Usage) llm.Usage {
	usage.Cost = llm.EstimateCost(pricing, usage)
	return usage
}

// RecomputeTotal sets TotalTokens from the token components and assigns cost.
func RecomputeTotal(pricing llm.Pricing, usage llm.Usage) llm.Usage {
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens +
		usage.CacheReadTokens + usage.CacheWriteTokens
	return AssignCost(pricing, usage)
}

// UsageFromReport normalizes provider-reported token counts, splitting cache
// tokens out of the reported input count, and assigns cost.
func UsageFromReport(
	pricing llm.Pricing,
	input, output, reasoning, cacheRead, cacheWrite, total int64,
) llm.Usage {
	input -= cacheRead + cacheWrite
	if input < 0 {
		input = 0
	}
	return AssignCost(pricing, llm.Usage{
		InputTokens:      input,
		OutputTokens:     output,
		ReasoningTokens:  reasoning,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		TotalTokens:      total,
	})
}
