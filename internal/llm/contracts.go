// Package llm defines provider-neutral language model contracts owned by AICE.
package llm

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// API identifies the wire protocol used to call a model.
// It is intentionally open-ended so adapters can introduce new protocols.
type API string

// ProviderID identifies a model provider.
// It is intentionally open-ended so providers do not require a central registry.
type ProviderID string

// Role identifies the author of a message.
type Role string

const (
	RoleUnknown    Role = ""
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
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

// Message is the closed set of provider-neutral messages understood by an LLM.
// Concrete messages retain all metadata needed for replay and persistence.
type Message interface {
	message()
}

// AgentMessage is one complete transcript message. It currently equals Message
// because AICE does not yet have custom transcript-only message types.
type AgentMessage = Message

// TextContent is visible text carried by a user or assistant message.
// Signature is opaque provider state that may need to be replayed unchanged.
type TextContent struct {
	Type      ContentType `json:"type"`
	Text      string      `json:"text"`
	Signature string      `json:"signature,omitempty"`
}

// ThinkingContent is model reasoning carried by an assistant message.
type ThinkingContent struct {
	Type      ContentType `json:"type"`
	Text      string      `json:"text"`
	Signature string      `json:"signature,omitempty"`
	Redacted  bool        `json:"redacted,omitempty"`
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

// UserMessage is a user-authored message before it is placed in model history.
type UserMessage struct {
	Role      Role          `json:"role"`
	Content   []ContentPart `json:"content"`
	Timestamp int64         `json:"timestamp"`
}

// AssistantMessage is the complete or partial result of one model request.
// It is the canonical value stored in history after streaming terminates.
type AssistantMessage struct {
	Role            Role          `json:"role"`
	Content         []ContentPart `json:"content"`
	API             API           `json:"api"`
	Provider        ProviderID    `json:"provider"`
	ModelID         string        `json:"model"`
	ResponseModelID string        `json:"response_model,omitempty"`
	ResponseID      string        `json:"response_id,omitempty"`
	Usage           Usage         `json:"usage"`
	StopReason      StopReason    `json:"stop_reason"`
	ErrorMessage    string        `json:"error_message,omitempty"`
	Timestamp       int64         `json:"timestamp"`
}

// ToolResultMessage is the history message produced by one tool execution.
type ToolResultMessage struct {
	Role       Role          `json:"role"`
	ToolCallID string        `json:"tool_call_id"`
	ToolName   string        `json:"tool_name,omitempty"`
	Content    []ContentPart `json:"content"`
	IsError    bool          `json:"is_error,omitempty"`
	Timestamp  int64         `json:"timestamp"`
}

// NewTextContent constructs visible text content.
func NewTextContent(text string) TextContent {
	return TextContent{Type: ContentTypeText, Text: text}
}

// Part converts text content into the common tagged content representation.
func (c TextContent) Part() ContentPart {
	return ContentPart{
		Type:      c.Type,
		Text:      c.Text,
		Signature: c.Signature,
	}
}

// NewThinkingContent constructs assistant reasoning content.
func NewThinkingContent(text, signature string) ThinkingContent {
	return ThinkingContent{
		Type:      ContentTypeThinking,
		Text:      text,
		Signature: signature,
	}
}

// Part converts thinking content into the common tagged content representation.
func (c ThinkingContent) Part() ContentPart {
	return ContentPart{
		Type:      c.Type,
		Text:      c.Text,
		Signature: c.Signature,
		Redacted:  c.Redacted,
	}
}

// NewUserMessage constructs and validates a user message.
func NewUserMessage(content ...ContentPart) (UserMessage, error) {
	message := UserMessage{
		Role:      RoleUser,
		Content:   slices.Clone(content),
		Timestamp: time.Now().UnixMilli(),
	}
	if err := message.Validate(); err != nil {
		return UserMessage{}, err
	}
	return message, nil
}

func (UserMessage) message() {}

// Validate checks that a user message contains only user-supported content.
func (m UserMessage) Validate() error {
	if m.Role != RoleUser {
		return fmt.Errorf("llm: user message has role %q", m.Role)
	}
	return validateMessageContent(m.Role, m.Content, false)
}

// NewAssistantMessage creates the initial partial result for a model stream.
func NewAssistantMessage(model Model) AssistantMessage {
	return AssistantMessage{
		Role:      RoleAssistant,
		Content:   []ContentPart{},
		API:       model.API,
		Provider:  model.Provider,
		ModelID:   model.ID,
		Timestamp: time.Now().UnixMilli(),
	}
}

func (AssistantMessage) message() {}

// Validate checks assistant identity, metadata, and content invariants.
func (m AssistantMessage) Validate() error {
	if m.Role != RoleAssistant {
		return fmt.Errorf("llm: assistant message has role %q", m.Role)
	}
	if m.API == "" || m.Provider == "" || m.ModelID == "" {
		return fmt.Errorf("llm: assistant message api, provider, and model are required")
	}
	return validateMessageContent(m.Role, m.Content, true)
}

// NewToolResultMessage constructs and validates one tool-result message.
func NewToolResultMessage(result ToolResult) (ToolResultMessage, error) {
	message := ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: result.CallID,
		ToolName:   result.Name,
		Content:    slices.Clone(result.Content),
		IsError:    result.IsError,
		Timestamp:  time.Now().UnixMilli(),
	}
	if err := message.Validate(); err != nil {
		return ToolResultMessage{}, err
	}
	return message, nil
}

func (ToolResultMessage) message() {}

// Validate checks that a tool-result message can be replayed safely.
func (m ToolResultMessage) Validate() error {
	if m.Role != RoleToolResult {
		return fmt.Errorf("llm: tool result message has role %q", m.Role)
	}
	return ToolResult{
		CallID:  m.ToolCallID,
		Name:    m.ToolName,
		Content: m.Content,
		IsError: m.IsError,
	}.Validate()
}

func validateMessage(message Message) error {
	switch value := message.(type) {
	case UserMessage:
		return value.Validate()
	case AssistantMessage:
		return value.Validate()
	case ToolResultMessage:
		return value.Validate()
	case nil:
		return fmt.Errorf("llm: message is nil")
	default:
		return fmt.Errorf("llm: unsupported message type %T", message)
	}
}

func validateMessageContent(role Role, content []ContentPart, allowEmpty bool) error {
	if !allowEmpty && len(content) == 0 {
		return fmt.Errorf("llm: %s message content is empty", role)
	}
	for index, part := range content {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("llm: content %d: %w", index, err)
		}
		if !contentAllowedForRole(role, part.Type) {
			return fmt.Errorf("llm: content type %q is not allowed for role %q", part.Type, role)
		}
	}
	return nil
}

// Validate rejects missing payloads and conflicting tagged content fields.
func (p ContentPart) Validate() error {
	switch p.Type {
	case ContentTypeText:
		if p.Redacted || p.Image != nil || p.ToolCall != nil || p.ToolResult != nil {
			return fmt.Errorf("text content has conflicting payload fields")
		}
	case ContentTypeThinking:
		if p.Image != nil || p.ToolCall != nil || p.ToolResult != nil {
			return fmt.Errorf("thinking content has conflicting payload fields")
		}
	case ContentTypeImage:
		if p.Image == nil {
			return fmt.Errorf("image content payload is required")
		}
		if p.Text != "" || p.Signature != "" || p.Redacted || p.ToolCall != nil || p.ToolResult != nil {
			return fmt.Errorf("image content has conflicting payload fields")
		}
		if len(p.Image.Data) == 0 || p.Image.MIMEType == "" {
			return fmt.Errorf("image content data and mime type are required")
		}
	case ContentTypeToolCall:
		if p.ToolCall == nil {
			return fmt.Errorf("tool call payload is required")
		}
		if p.Text != "" || p.Signature != "" || p.Redacted || p.Image != nil || p.ToolResult != nil {
			return fmt.Errorf("tool call content has conflicting payload fields")
		}
		if p.ToolCall.ID == "" || p.ToolCall.Name == "" {
			return fmt.Errorf("tool call id and name are required")
		}
		if !json.Valid(p.ToolCall.Arguments) {
			return fmt.Errorf("tool call arguments are not valid json")
		}
	case ContentTypeToolResult:
		if p.ToolResult == nil {
			return fmt.Errorf("tool result payload is required")
		}
		if p.Text != "" || p.Signature != "" || p.Redacted || p.Image != nil || p.ToolCall != nil {
			return fmt.Errorf("tool result content has conflicting payload fields")
		}
		if err := p.ToolResult.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported content type %q", p.Type)
	}
	return nil
}

// Validate checks the identity and nested content of a tool result.
func (r ToolResult) Validate() error {
	if r.CallID == "" {
		return fmt.Errorf("tool result call id is required")
	}
	for index, part := range r.Content {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("tool result content %d: %w", index, err)
		}
		if part.Type != ContentTypeText && part.Type != ContentTypeImage {
			return fmt.Errorf("tool result content %d has unsupported type %q", index, part.Type)
		}
	}
	return nil
}

func contentAllowedForRole(role Role, contentType ContentType) bool {
	switch role {
	case RoleUser:
		return contentType == ContentTypeText || contentType == ContentTypeImage
	case RoleAssistant:
		return contentType == ContentTypeText ||
			contentType == ContentTypeThinking ||
			contentType == ContentTypeToolCall
	default:
		return false
	}
}

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

// UnmarshalJSON restores each message's concrete type from its role.
func (r *Request) UnmarshalJSON(data []byte) error {
	var raw struct {
		Model        Model             `json:"model"`
		SystemPrompt string            `json:"system_prompt,omitempty"`
		Messages     []json.RawMessage `json:"messages"`
		Tools        []ToolDefinition  `json:"tools,omitempty"`
		Options      StreamOptions     `json:"options"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("llm: decode request: %w", err)
	}

	messages := make([]Message, len(raw.Messages))
	for index, encoded := range raw.Messages {
		message, err := unmarshalMessage(encoded)
		if err != nil {
			return fmt.Errorf("llm: decode request message %d: %w", index, err)
		}
		messages[index] = message
	}

	*r = Request{
		Model:        raw.Model,
		SystemPrompt: raw.SystemPrompt,
		Messages:     messages,
		Tools:        raw.Tools,
		Options:      raw.Options,
	}
	return nil
}

func unmarshalMessage(data []byte) (Message, error) {
	var envelope struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode role: %w", err)
	}

	switch envelope.Role {
	case RoleUser:
		var message UserMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		return message, nil
	case RoleAssistant:
		var message AssistantMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		return message, nil
	case RoleToolResult:
		var message ToolResultMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		return message, nil
	default:
		return nil, fmt.Errorf("unsupported role %q", envelope.Role)
	}
}

// Validate checks provider-neutral request invariants. Protocol adapters and
// providers remain responsible for their own capability and compatibility
// restrictions.
func (r Request) Validate() error {
	if strings.TrimSpace(r.Model.ID) == "" {
		return fmt.Errorf("llm: request model id is required")
	}
	if strings.TrimSpace(string(r.Model.API)) == "" {
		return fmt.Errorf("llm: request model api is required")
	}
	if strings.TrimSpace(string(r.Model.Provider)) == "" {
		return fmt.Errorf("llm: request model provider is required")
	}
	if r.Model.ContextWindow < 0 {
		return fmt.Errorf("llm: request model context window cannot be negative")
	}
	if r.Model.MaxTokens < 0 {
		return fmt.Errorf("llm: request model max tokens cannot be negative")
	}
	if err := r.Options.validate(r.Model.MaxTokens); err != nil {
		return err
	}

	if len(r.Messages) == 0 {
		return fmt.Errorf("llm: request at least one message is required")
	}
	for index, message := range r.Messages {
		if err := validateMessage(message); err != nil {
			return fmt.Errorf("llm: request message %d: %w", index, err)
		}
	}

	toolNames := make(map[string]struct{}, len(r.Tools))
	for index, tool := range r.Tools {
		if err := tool.validate(); err != nil {
			return fmt.Errorf("llm: request tool %d: %w", index, err)
		}
		if _, exists := toolNames[tool.Name]; exists {
			return fmt.Errorf("llm: request duplicate tool name %q", tool.Name)
		}
		toolNames[tool.Name] = struct{}{}
	}

	return nil
}

func (o StreamOptions) validate(modelMaxTokens int64) error {
	if o.MaxTokens < 0 {
		return fmt.Errorf("llm: request max tokens cannot be negative")
	}
	maxTokens := o.MaxTokens
	if maxTokens == 0 {
		maxTokens = modelMaxTokens
	}
	if maxTokens <= 0 {
		return fmt.Errorf("llm: request max tokens must be positive")
	}

	if o.Temperature != nil {
		if math.IsNaN(*o.Temperature) || math.IsInf(*o.Temperature, 0) {
			return fmt.Errorf("llm: request temperature must be finite")
		}
		if *o.Temperature < 0 {
			return fmt.Errorf("llm: request temperature cannot be negative")
		}
	}

	switch o.Thinking {
	case ThinkingLevelUnknown,
		ThinkingLevelOff,
		ThinkingLevelMinimal,
		ThinkingLevelLow,
		ThinkingLevelMedium,
		ThinkingLevelHigh,
		ThinkingLevelXHigh,
		ThinkingLevelMax:
		return nil
	default:
		return fmt.Errorf("llm: request unsupported thinking level %q", o.Thinking)
	}
}

func (t ToolDefinition) validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("name is required")
	}

	var schema map[string]json.RawMessage
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
		return fmt.Errorf("tool %q input schema must be a json object: %w", t.Name, err)
	}
	if schema == nil {
		return fmt.Errorf("tool %q input schema must be a json object", t.Name)
	}
	return nil
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
	EventTypeError         EventType = "error"
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
	Type          EventType         `json:"type"`
	ContentIndex  int               `json:"content_index"`
	Delta         string            `json:"delta,omitempty"`
	Content       *ContentPart      `json:"content,omitempty"`
	ToolCallDelta *ToolCallDelta    `json:"tool_call_delta,omitempty"`
	ToolCall      *ToolCall         `json:"tool_call,omitempty"`
	Usage         *Usage            `json:"usage,omitempty"`
	StopReason    StopReason        `json:"stop_reason,omitempty"`
	Message       *AssistantMessage `json:"message,omitempty"`
	Err           error             `json:"-"`
}

// Stream yields model events in order and observes the context used to create it.
// Next returns io.EOF after a done or error terminal event. Errors returned
// directly by Next indicate that no normalized terminal event could be built.
type Stream interface {
	Next() (Event, error)
	Close() error
}
