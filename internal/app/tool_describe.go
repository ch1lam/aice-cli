package app

import (
	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// toolDescriber projects a call into the shared tool vocabulary from the
// Guard's normalized actions: before the decision from extraction, after it
// from the actions recorded on the result.
type toolDescriber struct {
	guard     *guard.Guard
	workspace string
}

func (d toolDescriber) describe(call llm.ToolCall, result *llm.ToolResultMessage) (interaction.ToolKind, []interaction.ToolLocation) {
	actions := recordedActions(d.guard.Actions(call))
	if result != nil {
		actions = result.Actions
	}
	return interaction.ToolKindOf(actions), interaction.ToolLocations(actions, d.workspace)
}

// annotate fills the vocabulary of a translated tool event.
func (d toolDescriber) annotate(event agent.AgentEvent, display *interaction.Event) {
	if event.ToolCall == nil || display.Kind != interaction.EventToolStart && display.Kind != interaction.EventToolEnd {
		return
	}
	display.Tool.Kind, display.Tool.Locations = d.describe(*event.ToolCall, event.ToolResult)
}

// display translates an Agent event for a frontend, with the shared tool
// vocabulary and the run's MCP tool labels; nil for events frontends do not
// show.
func (d toolDescriber) display(event agent.AgentEvent, catalog *mcpCatalog) *interaction.Event {
	display := translateAgentEvent(event)
	if display == nil {
		return nil
	}
	d.annotate(event, display)
	if label, ok := catalog.displayName(display.Tool.Name); ok && display.Kind == interaction.EventToolStart {
		display.Tool.Name = label
	}
	return display
}
