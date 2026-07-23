// Package agent defines the boundaries used by AICE's agent loop.
package agent

import (
	"context"
	"errors"

	"github.com/ch1lam/aice-cli/internal/llm"
)

var (
	// ErrProtocol indicates that a model stream violated the llm stream contract.
	ErrProtocol = errors.New("agent: invalid model stream")
	// ErrTurnLimit indicates that another model request would exceed MaxTurns.
	ErrTurnLimit = errors.New("agent: maximum turns reached")
	// ErrToolStepLimit indicates that another tool call would exceed MaxToolSteps.
	ErrToolStepLimit = errors.New("agent: maximum tool steps reached")
	// ErrContextLimit indicates that another model request would leave too
	// little room in the model context window.
	ErrContextLimit = errors.New("agent: context limit reached")
)

// Model is the language model capability consumed by the agent loop.
type Model interface {
	Stream(ctx context.Context, request llm.Request) (llm.Stream, error)
}

// Tool is an executable capability supplied to the agent loop.
type Tool interface {
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error)
}

// Limits bounds work performed by one agent run.
type Limits struct {
	MaxTurns     int
	MaxToolSteps int
}

// RunInput contains the caller-owned state needed for one agent run.
type RunInput struct {
	Model        llm.Model
	SystemPrompt string
	History      []llm.AgentMessage
	Prompt       llm.UserMessage
	Options      llm.StreamOptions
}

// Turn contains one completed assistant response and its ordered tool results.
type Turn struct {
	Number      int
	Assistant   llm.AssistantMessage
	ToolResults []llm.ToolResultMessage
}

// Result contains only the messages produced by this run. The caller remains
// responsible for the history supplied in RunInput.
type Result struct {
	Prompt llm.UserMessage
	Turns  []Turn
}

// Messages returns the complete transcript messages produced by this run.
func (r Result) Messages() []llm.AgentMessage {
	count := 1
	for _, turn := range r.Turns {
		count += 1 + len(turn.ToolResults)
	}

	messages := make([]llm.AgentMessage, 0, count)
	messages = append(messages, r.Prompt)
	for _, turn := range r.Turns {
		messages = append(messages, turn.Assistant)
		for _, toolResult := range turn.ToolResults {
			messages = append(messages, toolResult)
		}
	}
	return messages
}

// EventType identifies one agent lifecycle event.
type EventType string

const (
	EventTypeUnknown            EventType = ""
	EventTypeAgentStart         EventType = "agent_start"
	EventTypeAgentEnd           EventType = "agent_end"
	EventTypeTurnStart          EventType = "turn_start"
	EventTypeTurnEnd            EventType = "turn_end"
	EventTypeMessageStart       EventType = "message_start"
	EventTypeMessageUpdate      EventType = "message_update"
	EventTypeMessageEnd         EventType = "message_end"
	EventTypeToolExecutionStart EventType = "tool_execution_start"
	EventTypeToolExecutionEnd   EventType = "tool_execution_end"
)

// AgentEvent is one synchronous lifecycle update from an agent run. Fields are
// populated according to Type; TurnNumber is one-based.
type AgentEvent struct {
	Type       EventType
	TurnNumber int
	// Message is populated for message_start, message_end, and turn_end.
	Message llm.AgentMessage
	// AssistantMessageEvent is populated only for message_update.
	AssistantMessageEvent *llm.Event
	ToolCall              *llm.ToolCall
	ToolResult            *llm.ToolResultMessage
	// ToolResults is populated only for turn_end.
	ToolResults []llm.ToolResultMessage
	// Messages contains this run's new transcript messages on agent_end.
	Messages []llm.AgentMessage
	Err      error
}

// AgentEventSink receives lifecycle events in order. Returning an error stops
// the run immediately.
type AgentEventSink func(ctx context.Context, event AgentEvent) error
