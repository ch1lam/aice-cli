// Package llm defines provider-neutral language model contracts owned by AICE.
package llm

import "encoding/json"

// Role identifies the author of a message.
type Role string

const (
	RoleUnknown   Role = ""
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentType identifies the payload carried by a content part.
type ContentType string

const (
	ContentTypeUnknown    ContentType = ""
	ContentTypeText       ContentType = "text"
	ContentTypeReasoning  ContentType = "reasoning"
	ContentTypeToolCall   ContentType = "tool_call"
	ContentTypeToolResult ContentType = "tool_result"
)

// Message is the provider-neutral representation of one conversation message.
type Message struct {
	Role    Role
	Content []ContentPart
}

// ContentPart contains exactly one payload selected by Type.
type ContentPart struct {
	Type       ContentType
	Text       string
	ToolCall   *ToolCall
	ToolResult *ToolResult
}

// ToolCall is a complete, validated request to execute a tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult records the output associated with one tool call.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
}

// ToolDefinition describes a tool exposed to a model.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Model identifies a provider model and its relevant limits.
type Model struct {
	Provider        string
	ID              string
	ContextWindow   int64
	MaxOutputTokens int64
}

// Usage contains normalized token accounting for a model request.
type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CacheReadTokens int64
}

// StopReason explains why a model stream ended.
type StopReason string

const (
	StopReasonUnknown   StopReason = ""
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonCanceled  StopReason = "canceled"
)

// Request contains the provider-neutral input for one model stream.
type Request struct {
	Model    Model
	Messages []Message
	Tools    []ToolDefinition
}

// EventType identifies one normalized streaming event.
type EventType string

const (
	EventTypeUnknown        EventType = ""
	EventTypeTextDelta      EventType = "text_delta"
	EventTypeReasoningDelta EventType = "reasoning_delta"
	EventTypeToolCallDelta  EventType = "tool_call_delta"
	EventTypeUsage          EventType = "usage"
	EventTypeDone           EventType = "done"
)

// ToolCallDelta is one partial update for a streamed tool call.
type ToolCallDelta struct {
	Index          int
	ID             string
	Name           string
	ArgumentsDelta string
}

// Event is one provider-neutral item emitted by a model stream.
type Event struct {
	Type          EventType
	TextDelta     string
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	StopReason    StopReason
}

// Stream yields model events in order and observes the context used to create it.
// Next returns io.EOF after the final event.
type Stream interface {
	Next() (Event, error)
	Close() error
}
