// Package agent defines the boundaries used by AICE's agent loop.
package agent

import (
	"context"
	"errors"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
)

var (
	// ErrProtocol indicates that a model stream violated the llm stream contract.
	ErrProtocol = errors.New("agent: invalid model stream")
	// ErrContextLimit indicates that another model request would leave too
	// little room in the model context window.
	ErrContextLimit = errors.New("agent: context limit reached")
)

// Model is the language model capability consumed by the agent loop.
type Model interface {
	Stream(ctx context.Context, request llm.Request) (llm.Stream, error)
}

// ModelIdentity is the optional capability a Model may expose so the loop can
// verify that RunInput.Model metadata matches the service that serves it.
type ModelIdentity interface {
	ProviderID() llm.ProviderID
}

// Tool is an executable capability supplied to the agent loop.
type Tool interface {
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, call llm.ToolCall) (llm.ToolResult, error)
}

// InputMessage is one caller-owned user message waiting to be injected into
// an active run. ID is echoed on the corresponding message events.
type InputMessage struct {
	ID      string
	Message llm.UserMessage
}

// InputSource returns at most one waiting message without blocking.
type InputSource func() (InputMessage, bool, error)

// HistoryCompactor replaces complete transcript history when the next model
// request crosses the compaction threshold. The input never includes the next
// user message; the loop appends that message after compaction succeeds.
type HistoryCompactor func(
	ctx context.Context,
	history []llm.AgentMessage,
) ([]llm.AgentMessage, error)

// RunInput contains the caller-owned state needed for one agent run.
type RunInput struct {
	Model        llm.Model
	SystemPrompt string
	History      []llm.AgentMessage
	Prompt       llm.UserMessage
	Options      llm.StreamOptions
	// Compactor is called only at complete interaction boundaries. It is not
	// used while settling a tool call inside the current interaction.
	Compactor HistoryCompactor
	// Steering is polled only at safe boundaries after an assistant response
	// and all of that response's tool calls have matching results.
	Steering InputSource
	// FollowUp is polled only when the Agent would otherwise stop naturally.
	FollowUp InputSource
}

// Turn contains user input injected immediately before one completed assistant
// response, followed by that response and its ordered tool results.
type Turn struct {
	Number      int
	Inputs      []llm.UserMessage
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
		count += len(turn.Inputs) + 1 + len(turn.ToolResults)
	}

	messages := make([]llm.AgentMessage, 0, count)
	messages = append(messages, r.Prompt)
	for _, turn := range r.Turns {
		for _, input := range turn.Inputs {
			messages = append(messages, input)
		}
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
	EventTypeInteractionEnd     EventType = "interaction_end"
	EventTypeTurnStart          EventType = "turn_start"
	EventTypeTurnEnd            EventType = "turn_end"
	EventTypeMessageStart       EventType = "message_start"
	EventTypeMessageUpdate      EventType = "message_update"
	EventTypeMessageEnd         EventType = "message_end"
	EventTypeToolExecutionStart EventType = "tool_execution_start"
	EventTypeToolExecutionEnd   EventType = "tool_execution_end"
	EventTypeRetryStart         EventType = "retry_start"
	EventTypeRetryEnd           EventType = "retry_end"
)

// InputKind identifies why a user message entered an active run.
type InputKind uint8

const (
	InputKindUnknown InputKind = iota
	InputKindSteering
	InputKindFollowUp
)

// RetryEvent describes one model-call retry. Attempt is one-based and never
// exceeds MaxRetries. Success is meaningful only on retry_end.
type RetryEvent struct {
	Attempt    int
	MaxRetries int
	Delay      time.Duration
	Success    bool
	Err        error
}

// AgentEvent is one synchronous lifecycle update from an agent run. Fields are
// populated according to Type; TurnNumber is one-based.
type AgentEvent struct {
	Type       EventType
	TurnNumber int
	// InputID and InputKind are populated on message_start and message_end for
	// steering and follow-up input. They are empty for the initial user prompt.
	InputID   string
	InputKind InputKind
	// Message is populated for message_start, message_end, and turn_end.
	Message llm.AgentMessage
	// AssistantMessageEvent is populated only for message_update.
	AssistantMessageEvent *llm.Event
	ToolCall              *llm.ToolCall
	ToolResult            *llm.ToolResultMessage
	// Retry is populated only for retry_start and retry_end.
	Retry *RetryEvent
	// ToolResults is populated only for turn_end.
	ToolResults []llm.ToolResultMessage
	// Messages contains the completed interaction on interaction_end and all
	// new transcript messages on agent_end.
	Messages []llm.AgentMessage
	Err      error
}

// AgentEventSink receives lifecycle events in order. Returning an error stops
// the run immediately.
type AgentEventSink func(ctx context.Context, event AgentEvent) error
