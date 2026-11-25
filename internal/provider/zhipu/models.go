package zhipu

import (
	"github.com/ch1lam/aice-cli/internal/api/openaicompletions"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// Model facts follow BigModel's model overview, individual model pages and
// concept-param output limits (checked 2026-11-25). Only tool-capable chat
// models are included. Coding Plan has a separate availability list;
// historical aliases redirect. GLM-5.4 is Coding Plan only until its API
// opens. The overview omits GLM-4.6V-FlashX, but pricing and concept-param
// still list it.
type modelSpec struct {
	id, name                 string
	contextWindow, maxTokens int64
	image                    bool
	plans                    plans
}

// plans records which endpoints serve a model.
type plans uint8

const (
	platform plans = 1 << iota
	coding
	bothPlans = platform | coding
)

var modelSpecs = []modelSpec{
	// BigModel recommends a 512K client window on the Coding Plan for better
	// coding results; the model accepts 1M.
	{ModelGLM54, "GLM-5.4", 1_000_000, 131_072, true, coding},
	{ModelGLM53, "GLM-5.3", 1_000_000, 131_072, false, bothPlans},
	{ModelGLM53Flash, "GLM-5.3-Flash", 1_000_000, 131_072, true, bothPlans},
	{"glm-5.3-flashx", "GLM-5.3-FlashX", 1_000_000, 131_072, true, platform},
	{"glm-5.2", "GLM-5.2", 1_000_000, 131_072, false, platform},
	{"glm-5.1", "GLM-5.1", 200_000, 131_072, false, platform},
	{"glm-5", "GLM-5", 200_000, 131_072, false, platform},
	{"glm-5-turbo", "GLM-5-Turbo", 200_000, 131_072, false, platform},
	{"glm-4.7", "GLM-4.7", 200_000, 131_072, false, platform},
	{"glm-4.7-flashx", "GLM-4.7-FlashX", 200_000, 131_072, false, platform},
	{"glm-4.7-flash", "GLM-4.7-Flash", 200_000, 131_072, false, platform},
	{"glm-4.6", "GLM-4.6", 200_000, 131_072, false, platform},
	{"glm-4.5-air", "GLM-4.5-Air", 128_000, 98_304, false, platform},
	{"glm-4.5-airx", "GLM-4.5-AirX", 128_000, 98_304, false, platform},
	{"glm-4-flashx-250414", "GLM-4-FlashX-250414", 128_000, 16_384, false, platform},
	// The overview says 16K while concept-param allows 32K. Keep the lower budget.
	{"glm-4-flash-250414", "GLM-4-Flash-250414", 128_000, 16_384, false, platform},
	{"glm-5v-turbo", "GLM-5V-Turbo", 200_000, 131_072, true, platform},
	{"glm-4.6v", "GLM-4.6V", 128_000, 32_768, true, platform},
	{"glm-4.6v-flashx", "GLM-4.6V-FlashX", 128_000, 32_768, true, platform},
	{"glm-4.6v-flash", "GLM-4.6V-Flash", 128_000, 32_768, true, platform},
}

func catalogModel(spec modelSpec, providerID llm.ProviderID) llm.Model {
	result := llm.Model{
		ID: spec.id, Name: spec.name, Provider: providerID, API: openaicompletions.API,
		ContextWindow: spec.contextWindow, MaxTokens: spec.maxTokens,
		InputModalities:  []llm.InputModality{llm.InputModalityText},
		SupportsThinking: true,
		ThinkingFormat:   llm.ThinkingFormatDeepSeek,
		ThinkingLevelMap: llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh),
		// CNY platform prices and subscription quotas are not USD cost estimates.
	}
	if spec.image {
		result.InputModalities = append(result.InputModalities, llm.InputModalityImage)
	}
	switch spec.id {
	// Thinking is always on; BigModel maps a disabled request on GLM-5.4 to low.
	case ModelGLM54, ModelGLM53, ModelGLM53Flash, "glm-5.3-flashx":
		result.ThinkingLevelMap = llm.ThinkingLevelsMap(llm.ThinkingLevelLow, llm.ThinkingLevelHigh, llm.ThinkingLevelMax)
		result.SupportsReasoningEffort = true
	case "glm-5.2":
		result.ThinkingLevelMap = llm.ThinkingLevelsMap(llm.ThinkingLevelOff, llm.ThinkingLevelHigh, llm.ThinkingLevelMax)
		result.SupportsReasoningEffort = true
	case "glm-4-flashx-250414", "glm-4-flash-250414":
		result.SupportsThinking = false
		result.ThinkingFormat = ""
		result.ThinkingLevelMap = llm.ThinkingLevelsMap(llm.ThinkingLevelOff)
	}
	return result
}
