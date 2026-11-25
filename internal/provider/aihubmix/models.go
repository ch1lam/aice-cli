package aihubmix

import (
	"github.com/ch1lam/aice-cli/internal/api/anthropic"
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/api/openairesponses"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Models returns independent catalog values. Facts come from AiHubMix's public
// /api/v1/models and /model-data/index.json (checked 2026-11-25), not
// direct-vendor catalogs. Prices are base-tier USD estimates, excluding
// long-context tiers. GPT-6.2 effort levels follow pi's OpenAI family rules:
// AiHubMix's index marks their full set as not yet verified.
func Models() []llm.Model {
	return []llm.Model{
		gptModel(
			"gpt-6.2-sol",
			"GPT 6.2 Sol",
			llm.Pricing{Input: 2, Output: 10, CacheRead: .1, CacheWrite: 2.5},
			false,
		),
		gptModel(
			"gpt-6-luna",
			"GPT 6 Luna",
			llm.Pricing{Input: .1, Output: .5, CacheRead: .01, CacheWrite: .125},
			true,
		),
		gptModel(
			"gpt-6.2-astra",
			"GPT 6.2 Astra",
			llm.Pricing{Input: 8, Output: 40, CacheRead: .4, CacheWrite: 10},
			false,
		),
		claudeModel(
			"claude-sonnet-5-5",
			"Claude Sonnet 5.5",
			llm.Pricing{Input: 2, Output: 10, CacheRead: .1, CacheWrite: 2.5},
			false,
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
			// AiHubMix's listed rate may include promotions; billing may change.
			Pricing: llm.Pricing{Input: .155, Output: .62, CacheRead: .0031},
		},
		{
			ID: "kimi-k3.1-pro", Name: "Kimi K3.1 Pro", Provider: ProviderID, API: openaicompletions.API,
			ContextWindow: 1_048_576, MaxTokens: 131_072,
			InputModalities:  []llm.InputModality{llm.InputModalityText, llm.InputModalityImage},
			SupportsThinking: true,
			ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax),
			// AICE uses a bounded output budget below the gateway's advertised maximum.
			Pricing: llm.Pricing{Input: 2, Output: 10, CacheRead: .2},
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

// DefaultModel selects the coding-oriented GPT 6.2 Sol route.
func DefaultModel() llm.Model { return Models()[0] }
