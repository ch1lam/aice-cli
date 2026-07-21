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
	History      []llm.Message
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

// Messages converts the run result into replayable model history.
func (r Result) Messages() []llm.Message {
	count := 1
	for _, turn := range r.Turns {
		count += 1 + len(turn.ToolResults)
	}

	messages := make([]llm.Message, 0, count)
	messages = append(messages, r.Prompt.Message())
	for _, turn := range r.Turns {
		messages = append(messages, turn.Assistant.Message())
		for _, toolResult := range turn.ToolResults {
			messages = append(messages, toolResult.Message())
		}
	}
	return messages
}

// EventType identifies one agent lifecycle event.
type EventType string

const (
	EventTypeUnknown            EventType = ""
	EventTypeRunStart           EventType = "run_start"
	EventTypeRunEnd             EventType = "run_end"
	EventTypeTurnStart          EventType = "turn_start"
	EventTypeTurnEnd            EventType = "turn_end"
	EventTypeMessageStart       EventType = "message_start"
	EventTypeMessageUpdate      EventType = "message_update"
	EventTypeMessageEnd         EventType = "message_end"
	EventTypeToolExecutionStart EventType = "tool_execution_start"
	EventTypeToolExecutionEnd   EventType = "tool_execution_end"
)

// Event is one synchronous lifecycle update from a run. Fields are populated
// according to Type; TurnNumber is one-based.
type Event struct {
	Type             EventType
	TurnNumber       int
	UserMessage      *llm.UserMessage
	AssistantMessage *llm.AssistantMessage
	StreamEvent      *llm.Event
	ToolCall         *llm.ToolCall
	ToolResult       *llm.ToolResultMessage
	CompletedTurn    *Turn
	Result           *Result
	Err              error
}

// EventSink receives lifecycle events in order. Returning an error stops the
// run immediately.
type EventSink func(ctx context.Context, event Event) error
