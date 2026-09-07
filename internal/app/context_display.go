package app

import (
	"fmt"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/custom"
)

func applyContextWindow(model llm.Model, configuration config.Config) llm.Model {
	if window := configuration.ContextWindows[string(model.Provider)+"/"+model.ID]; window > 0 {
		model.ContextWindow = window
	}
	return model
}

func displayContextWindow(model llm.Model, configuration config.Config) int64 {
	if window := configuration.ContextWindows[string(model.Provider)+"/"+model.ID]; window > 0 {
		return window
	}
	return model.ContextWindow
}

func contextWindowInformation(model llm.Model, configuration config.Config) string {
	window := displayContextWindow(model, configuration)
	if window <= 0 {
		return fmt.Sprintf("Context window: unknown (local budget %d; configure context_windows)", model.ContextWindow)
	}
	source := "provider catalog default"
	if model.Provider == custom.ProviderID {
		source = "custom fallback default"
	}
	if configuration.ContextWindows[string(model.Provider)+"/"+model.ID] > 0 {
		source = "context_windows override"
	}
	return fmt.Sprintf("Context window: %d tokens (%s)", window, source)
}

func (s *interactiveSession) contextSnapshot() interaction.DisplayContext {
	settings := s.settingsSnapshot()
	return s.contextSnapshotFor(settings.model, settings.configuration, settings.systemPrompt)
}

func (s *interactiveSession) contextSnapshotFor(
	model llm.Model,
	configuration config.Config,
	systemPrompt string,
) interaction.DisplayContext {
	history, err := s.conversation.sideSnapshot()
	if err != nil {
		return interaction.DisplayContext{Window: displayContextWindow(model, configuration)}
	}
	return contextDisplay(model, configuration, systemPrompt, s.tools, history)
}

func contextDisplay(
	model llm.Model,
	configuration config.Config,
	systemPrompt string,
	tools []agent.Tool,
	history []llm.AgentMessage,
) interaction.DisplayContext {
	display := interaction.DisplayContext{Window: displayContextWindow(model, configuration)}
	messages, err := llm.AgentMessagesToMessages(history)
	if err != nil {
		return display
	}
	definitions := make([]llm.ToolDefinition, len(tools))
	for index, tool := range tools {
		definitions[index] = tool.Definition()
	}
	estimate := llm.EstimateContextTokens(llm.Request{
		Model: model, SystemPrompt: systemPrompt, Tools: definitions, Messages: messages,
	})
	display.Tokens = estimate.Tokens
	display.Known = true
	display.Estimated = estimate.LastUsageIndex < 0 || estimate.TrailingTokens > 0
	return display
}
