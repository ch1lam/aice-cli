// Package llm defines provider-neutral language model contracts owned by AICE.
package llm

import "encoding/json"

// API identifies the wire protocol used to call a model.
// It is intentionally open-ended so adapters can introduce new protocols.
type API string

// ProviderID identifies a model provider.
// It is intentionally open-ended so providers do not require a central registry.
type ProviderID string

// Role identifies the author of a message.
type Role string

const (
	RoleUnknown   Role = ""
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentType identifies the payload carried by a content part.
type ContentType string

const (
	ContentTypeUnknown    ContentType = ""
	ContentTypeText       ContentType = "text"
	ContentTypeThinking   ContentType = "thinking"
	ContentTypeImage      ContentType = "image"
	ContentTypeToolCall   ContentType = "tool_call"
	ContentTypeToolResult ContentType = "tool_result"
)

// ThinkingLevel is the requested amount of model reasoning.
type ThinkingLevel string

const (
	ThinkingLevelUnknown ThinkingLevel = ""
	ThinkingLevelOff     ThinkingLevel = "off"
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
	ThinkingLevelMax     ThinkingLevel = "max"
)

// InputModality identifies content a model can accept.
type InputModality string

const (
	InputModalityUnknown InputModality = ""
	InputModalityText    InputModality = "text"
	InputModalityImage   InputModality = "image"
)

// Message is one provider-neutral conversation message.
type Message struct {
	Role    Role          `json:"role"`
	Content []ContentPart `json:"content"`
}

// ContentPart is a tagged content value. Type selects the relevant payload.
// Text carries both visible text and thinking text. Signature is opaque provider
// state that may need to be sent back unchanged on a later request.
type ContentPart struct {
	Type       ContentType   `json:"type"`
	Text       string        `json:"text,omitempty"`
	Signature  string        `json:"signature,omitempty"`
	Redacted   bool          `json:"redacted,omitempty"`
	Image      *ImageContent `json:"image,omitempty"`
	ToolCall   *ToolCall     `json:"tool_call,omitempty"`
	ToolResult *ToolResult   `json:"tool_result,omitempty"`
}

// ImageContent contains inline image data.
type ImageContent struct {
	Data     []byte `json:"data"`
	MIMEType string `json:"mime_type"`
}

// ToolCall is a complete request to execute a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Signature string          `json:"signature,omitempty"`
}

// ToolResult records the output associated with one tool call.
type ToolResult struct {
	CallID  string        `json:"call_id"`
	Name    string        `json:"name,omitempty"`
	Content []ContentPart `json:"content"`
	IsError bool          `json:"is_error,omitempty"`
}

// ToolDefinition describes a tool exposed to a model.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Model identifies a provider model and the capabilities relevant to AICE.
// Connection details and credentials belong to provider configuration, not here.
type Model struct {
	ID               string          `json:"id"`
	Name             string          `json:"name,omitempty"`
	API              API             `json:"api"`
	Provider         ProviderID      `json:"provider"`
	SupportsThinking bool            `json:"supports_thinking,omitempty"`
	InputModalities  []InputModality `json:"input_modalities,omitempty"`
	ContextWindow    int64           `json:"context_window,omitempty"`
	MaxTokens        int64           `json:"max_tokens,omitempty"`
}

// Cost contains normalized request cost amounts in US dollars.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Total      float64 `json:"total"`
}

// Usage contains normalized token accounting for a model request.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens"`
	Cost             *Cost `json:"cost,omitempty"`
}

// StopReason explains why model generation ended.
type StopReason string

const (
	StopReasonUnknown StopReason = ""
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "tool_use"
	StopReasonPause   StopReason = "pause"
	StopReasonRefusal StopReason = "refusal"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// StreamOptions contains provider-neutral generation controls.
// A nil Temperature means the provider should use its default.
type StreamOptions struct {
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int64         `json:"max_tokens,omitempty"`
	Thinking    ThinkingLevel `json:"thinking,omitempty"`
}

// Request contains the provider-neutral input for one model stream.
// SystemPrompt is separate from message history so adapters can map it to each
// provider's preferred system or developer instruction representation.
type Request struct {
	Model        Model            `json:"model"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Messages     []Message        `json:"messages"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
	Options      StreamOptions    `json:"options"`
}

// EventType identifies one normalized streaming event.
type EventType string

const (
	EventTypeUnknown       EventType = ""
	EventTypeStart         EventType = "start"
	EventTypeTextStart     EventType = "text_start"
	EventTypeTextDelta     EventType = "text_delta"
	EventTypeTextEnd       EventType = "text_end"
	EventTypeThinkingStart EventType = "thinking_start"
	EventTypeThinkingDelta EventType = "thinking_delta"
	EventTypeThinkingEnd   EventType = "thinking_end"
	EventTypeToolCallStart EventType = "tool_call_start"
	EventTypeToolCallDelta EventType = "tool_call_delta"
	EventTypeToolCallEnd   EventType = "tool_call_end"
	EventTypeUsage         EventType = "usage"
	EventTypeDone          EventType = "done"
)

// ToolCallDelta is one partial update for a streamed tool call.
type ToolCallDelta struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

// Event is one tagged provider-neutral item emitted by a model stream.
// ContentIndex associates content events with their position in the assistant
// message. Content is populated on content-end events when an adapter needs to
// preserve opaque state such as a thinking signature. ToolCall is populated
// only when a complete call has been assembled.
type Event struct {
	Type          EventType      `json:"type"`
	ContentIndex  int            `json:"content_index,omitempty"`
	Delta         string         `json:"delta,omitempty"`
	Content       *ContentPart   `json:"content,omitempty"`
	ToolCallDelta *ToolCallDelta `json:"tool_call_delta,omitempty"`
	ToolCall      *ToolCall      `json:"tool_call,omitempty"`
	Usage         *Usage         `json:"usage,omitempty"`
	StopReason    StopReason     `json:"stop_reason,omitempty"`
}

// Stream yields model events in order and observes the context used to create it.
// Next returns io.EOF after the terminal event. Other errors terminate the
// stream; callers may retain events that were emitted before the failure.
type Stream interface {
	Next() (Event, error)
	Close() error
}
