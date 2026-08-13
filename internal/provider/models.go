package provider

import (
	"slices"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// ModelSpec is the provider-neutral specification shared by provider catalogs.
type ModelSpec struct {
	ID                      string
	Name                    string
	ContextWindow           int64
	MaxTokens               int64
	Input                   float64
	Output                  float64
	CacheRead               float64
	ThinkingLevelMap        llm.ThinkingLevelMap
	ThinkingFormat          llm.ThinkingFormat
	SupportsReasoningEffort bool
}

// deepSeekModelSpecs declares the DeepSeek V4 model specifications once so the
// DeepSeek and OpenCode Go catalogs share identical rates, limits, and effort
// choices.
var deepSeekModelSpecs = []ModelSpec{
	{
		ID:            "deepseek-v4-flash",
		Name:          "DeepSeek V4 Flash",
		ContextWindow: 1_000_000,
		MaxTokens:     384_000,
		Input:         0.14,
		Output:        0.28,
		CacheRead:     0.0028,
		// DeepSeek exposes three actual effort values plus off. Medium and
		// xhigh both map to high, so they are not separate choices.
		ThinkingLevelMap: deepSeekThinkingLevelMap(),
	},
	{
		ID:               "deepseek-v4-pro",
		Name:             "DeepSeek V4 Pro",
		ContextWindow:    1_000_000,
		MaxTokens:        384_000,
		Input:            0.435,
		Output:           0.87,
		CacheRead:        0.003625,
		ThinkingLevelMap: deepSeekThinkingLevelMap(),
	},
}

func deepSeekThinkingLevelMap() llm.ThinkingLevelMap {
	levels := llm.ThinkingLevelsMap(
		llm.ThinkingLevelOff,
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	)
	levels[llm.ThinkingLevelMedium] = llm.ThinkingValue("high")
	levels[llm.ThinkingLevelXHigh] = llm.ThinkingValue("high")
	return levels
}

// DeepSeekModelSpecs returns a copy of the shared DeepSeek model
// specifications.
func DeepSeekModelSpecs() []ModelSpec {
	specs := slices.Clone(deepSeekModelSpecs)
	for index := range specs {
		specs[index].ThinkingLevelMap = specs[index].ThinkingLevelMap.Clone()
	}
	return specs
}
