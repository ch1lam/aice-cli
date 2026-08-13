package provider

import (
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ModelSpec is the provider-neutral specification shared by provider catalogs.
type ModelSpec struct {
	ID             string
	Name           string
	ContextWindow  int64
	MaxTokens      int64
	Input          float64
	Output         float64
	CacheRead      float64
	ThinkingLevels []llm.ThinkingLevel
}

// deepSeekModelSpecs declares the DeepSeek V4 model specifications once so the
// DeepSeek and OpenCode Go catalogs share identical rates and limits.
var deepSeekModelSpecs = []ModelSpec{
	{
		ID:            "deepseek-v4-flash",
		Name:          "DeepSeek V4 Flash",
		ContextWindow: 1_000_000,
		MaxTokens:     384_000,
		Input:         0.14,
		Output:        0.28,
		CacheRead:     0.0028,
		// DeepSeek V4 exposes reasoning effort as off, high, and max only;
		// the minimal, low, and medium tiers are rejected (Pi's
		// deepseek thinkingLevelMap).
		ThinkingLevels: []llm.ThinkingLevel{
			llm.ThinkingLevelOff,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		},
	},
	{
		ID:            "deepseek-v4-pro",
		Name:          "DeepSeek V4 Pro",
		ContextWindow: 1_000_000,
		MaxTokens:     384_000,
		Input:         0.435,
		Output:        0.87,
		CacheRead:     0.003625,
		ThinkingLevels: []llm.ThinkingLevel{
			llm.ThinkingLevelOff,
			llm.ThinkingLevelHigh,
			llm.ThinkingLevelMax,
		},
	},
}

// DeepSeekModelSpecs returns a copy of the shared DeepSeek model
// specifications.
func DeepSeekModelSpecs() []ModelSpec {
	return slices.Clone(deepSeekModelSpecs)
}
