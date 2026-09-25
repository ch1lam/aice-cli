package aihubmix

import (
	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Models returns independent catalog values. Facts come from AiHubMix's public
// /api/v1/models and /model-data/index.json (2026-09-25), not direct-vendor
// catalogs. Prices are base-tier USD estimates, excluding long-context tiers.
func Models() []llm.Model {
	return []llm.Model{
		gptModel(
			"gpt-6-sol",
			"GPT 6 Sol",
			llm.Pricing{Input: 2, Output: 10, CacheRead: .2, CacheWrite: 2.5},
			true,
		),
		gptModel(
			"gpt-6-luna",
			"GPT 6 Luna",
			llm.Pricing{Input: .1, Output: .5, CacheRead: .01, CacheWrite: .125},
			true,
		),
		gptModel(
			"gpt-6-astra",
			"GPT 6 Astra",
			llm.Pricing{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5},
			false,
		),
		claudeModel(
			"claude-sonnet-5",
			"Claude Sonnet 5",
			llm.Pricing{Input: 2, Output: 10, CacheRead: .2, CacheWrite: 2.5},
			true,
		),
		claudeModel(
			"claude-opus-5-5",
			"Claude Opus 5.5",
			llm.Pricing{Input: 4, Output: 20, CacheRead: .2, CacheWrite: 5},
			false,
		),
		{
			ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash", Provider: ProviderID, API: openaicompletions.API,
			ContextWindow: 1_000_000, MaxTokens: 384_000,
			InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			SupportsThinking: true, ThinkingFormat: llm.ThinkingFormatDeepSeek, SupportsReasoningEffort: true,
			ThinkingLevelMap: llm.ThinkingLevelsMap(
				llm.ThinkingLevelOff,
				llm.ThinkingLevelLow,
				llm.ThinkingLevelHigh,
				llm.ThinkingLevelMax,
			),
			// AiHubMix's listed rate includes its current promotion; billing may change.
			Pricing: llm.Pricing{Input: .1408, Output: .5632, CacheRead: .002816},
		},
		{
			ID: "kimi-k3", Name: "Kimi K3", Provider: ProviderID, API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072,
			InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			SupportsThinking: true,
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			// AICE uses a bounded output budget below the gateway's advertised maximum.
			Pricing: llm.Pricing{Input: 3, Output: 15, CacheRead: .3},
		},
	}
}

func gptModel(id, name string, pricing llm.Pricing, supportsOff bool) llm.Model {
	model := reasoningModel(id, name, pricing, supportsOff)
	model.API = openairesponses.API
	model.ContextWindow = 1_050_000
	if supportsOff {
		model.ThinkingLevelMap[llm.ThinkingLevelOff] = llm.ThinkingValue("none")
	}
	return model
}

func claudeModel(id, name string, pricing llm.Pricing, supportsOff bool) llm.Model {
	model := reasoningModel(id, name, pricing, supportsOff)
	model.API = anthropic.API
	model.ThinkingFormat = llm.ThinkingFormatAdaptive
	model.SupportsReasoningEffort = true
	return model
}

func reasoningModel(id, name string, pricing llm.Pricing, supportsOff bool) llm.Model {
	levels := llm.ThinkingLevelsMap(
		llm.ThinkingLevelLow,
		llm.ThinkingLevelMedium,
		llm.ThinkingLevelHigh,
		llm.ThinkingLevelXHigh,
		llm.ThinkingLevelMax,
	)
	if supportsOff {
		levels[llm.ThinkingLevelOff] = llm.ThinkingValue("off")
	}
	return llm.Model{
		ID: id, Name: name, Provider: ProviderID, ContextWindow: 1_000_000, MaxTokens: 128_000,
		InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
		SupportsThinking: true, ThinkingLevelMap: levels, Pricing: pricing,
	}
}

func catalogModel(id string) (llm.Model, bool) {
	for _, model := range Models() {
		if model.ID == id {
			return model, true
		}
	}
	return llm.Model{}, false
}

// DefaultModel selects the coding-oriented GPT 6 Sol route.
func DefaultModel() llm.Model { return Models()[0] }
