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
	// RoleCompactionSummary identifies a derived transcript checkpoint. It is
	// projected to a user message only at the LLM request boundary.
	RoleCompactionSummary Role = "compactionSummary"
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

// DefaultThinkingLevel is the reasoning level used when none is requested,
// matching Pi's DEFAULT_THINKING_LEVEL. The effective level still clamps to
// what the selected model supports.
const DefaultThinkingLevel = ThinkingLevelMedium

// ThinkingFormat selects the wire shape a Chat Completions gateway expects
// for thinking controls. The empty value is the standard reasoning_effort
// field.
type ThinkingFormat string

const (
	// ThinkingFormatDeepSeek sends a DeepSeek-style thinking toggle object
	// (thinking: {type: enabled|disabled}) and, when the model declares
	// reasoning effort support, reasoning_effort.
	ThinkingFormatDeepSeek ThinkingFormat = "deepseek"
	// ThinkingFormatQwen sends a top-level enable_thinking boolean and, when
	// the model declares reasoning effort support, reasoning_effort.
	ThinkingFormatQwen ThinkingFormat = "qwen"
)

// thinkingLevelOrder ranks every thinking level from lowest to highest.
// Clamping aligns against this order.
var thinkingLevelOrder = []ThinkingLevel{
	ThinkingLevelOff,
	ThinkingLevelMinimal,
	ThinkingLevelLow,
	ThinkingLevelMedium,
	ThinkingLevelHigh,
	ThinkingLevelXHigh,
	ThinkingLevelMax,
}

// ThinkingLevelMap is a model's tri-state reasoning capability map, matching
// Pi's thinkingLevelMap semantics. A missing key uses the provider default:
// levels through high are supported with their canonical token, while xhigh
// and max are opt-in. A non-nil value overrides the provider wire token. An
// explicit nil value marks the level unsupported.
type ThinkingLevelMap map[ThinkingLevel]*string

// Supports reports whether the map accepts a canonical thinking level.
func (m ThinkingLevelMap) Supports(level ThinkingLevel) bool {
	if !slices.Contains(thinkingLevelOrder, level) {
		return false
	}
	value, exists := m[level]
	if exists {
		return value != nil
	}
	return level != ThinkingLevelXHigh && level != ThinkingLevelMax
}

// WireValue resolves the provider wire token for a level. Supported levels
// without an explicit value use the canonical token. The boolean reports
// whether the level is supported.
func (m ThinkingLevelMap) WireValue(level ThinkingLevel) (string, bool) {
	if !m.Supports(level) {
		return "", false
	}
	if value := m[level]; value != nil {
		return *value, true
	}
	return string(level), true
}

// Clone returns a deep copy that can be mutated independently.
func (m ThinkingLevelMap) Clone() ThinkingLevelMap {
	if m == nil {
		return nil
	}
	cloned := make(ThinkingLevelMap, len(m))
	for level, value := range m {
		if value == nil {
			cloned[level] = nil
			continue
		}
		copied := *value
		cloned[level] = &copied
	}
	return cloned
}

// ThinkingValue returns a pointer to a provider wire token for use in a
// ThinkingLevelMap. A nil map value is reserved for unsupported levels.
func ThinkingValue(value string) *string {
	return &value
}

// ThinkingLevelsMap builds an explicit map for exactly the given levels,
// using canonical wire tokens and marking every other canonical level
// unsupported.
func ThinkingLevelsMap(levels ...ThinkingLevel) ThinkingLevelMap {
	m := make(ThinkingLevelMap, len(thinkingLevelOrder))
	for _, level := range thinkingLevelOrder {
		m[level] = nil
	}
	for _, level := range levels {
		if slices.Contains(thinkingLevelOrder, level) {
			m[level] = ThinkingValue(string(level))
		}
	}
	return m
}

// StandardThinkingLevelMap returns the default capability map: off through
// high supported with canonical wire tokens, xhigh and max unsupported.
func StandardThinkingLevelMap() ThinkingLevelMap {
	return ThinkingLevelsMap(
		ThinkingLevelOff,
		ThinkingLevelMinimal,
		ThinkingLevelLow,
		ThinkingLevelMedium,
		ThinkingLevelHigh,
	)
}

// SupportedThinkingLevels derives the distinct canonical effort levels a
// model exposes. Multiple accepted inputs that map to the same canonical wire
// token collapse to one choice. Models without a declared map use Pi's
// defaults; models without thinking support expose only off.
func SupportedThinkingLevels(model Model) []ThinkingLevel {
	if !model.SupportsThinking {
		return []ThinkingLevel{ThinkingLevelOff}
	}

	effective := make(map[ThinkingLevel]struct{}, len(thinkingLevelOrder))
	for _, level := range thinkingLevelOrder {
		mapped, ok := effectiveThinkingLevel(model.ThinkingLevelMap, level)
		if ok {
			effective[mapped] = struct{}{}
		}
	}

	supported := make([]ThinkingLevel, 0, len(effective))
	for _, level := range thinkingLevelOrder {
		if _, ok := effective[level]; ok {
			supported = append(supported, level)
		}
	}
	return supported
}

func effectiveThinkingLevel(
	levelMap ThinkingLevelMap,
	level ThinkingLevel,
) (ThinkingLevel, bool) {
	value, ok := levelMap.WireValue(level)
	if !ok {
		return ThinkingLevelUnknown, false
	}
	mapped := ThinkingLevel(value)
	if mapped != level &&
		slices.Contains(thinkingLevelOrder, mapped) &&
		levelMap.Supports(mapped) {
		return mapped, true
	}
	return level, true
}

// ClampThinkingLevel aligns a requested level to an effective level a model
// exposes. A canonical wire mapping wins first; otherwise clamping prefers the
// next higher exposed level, then the next lower one, and finally the lowest
// exposed level. The unknown level is returned unchanged.
func ClampThinkingLevel(model Model, level ThinkingLevel) ThinkingLevel {
	if level == ThinkingLevelUnknown {
		return ThinkingLevelUnknown
	}
	supported := SupportedThinkingLevels(model)
	if slices.Contains(supported, level) {
		return level
	}
	if mapped, ok := effectiveThinkingLevel(model.ThinkingLevelMap, level); ok &&
		slices.Contains(supported, mapped) {
		return mapped
	}
	if len(supported) == 0 {
		return ThinkingLevelOff
	}
	index := slices.Index(thinkingLevelOrder, level)
	if index < 0 {
		return supported[0]
	}
	for _, candidate := range thinkingLevelOrder[index:] {
		if slices.Contains(supported, candidate) {
			return candidate
		}
	}
	for i := index - 1; i >= 0; i-- {
		if slices.Contains(supported, thinkingLevelOrder[i]) {
			return thinkingLevelOrder[i]
		}
	}
	return supported[0]
}

// InputModality identifies content a model can accept.
type InputModality string

const (
	InputModalityUnknown InputModality = ""
	InputModalityText    InputModality = "text"
	InputModalityImage   InputModality = "image"
)

// AgentMessage is the complete transcript-level message union.
type AgentMessage interface {
	MessageRole() Role
}

// Message is the closed set of provider-neutral messages understood by an LLM.
// Concrete messages retain all metadata needed for replay and persistence.
type Message interface {
	AgentMessage
	message()
}

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

// ToolDefinition describes a tool exposed to a model. PromptSnippet and
// PromptGuidelines are prompt presentation metadata used by the built-in
// system prompt; they are not part of the wire contract.
type ToolDefinition struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	InputSchema      json.RawMessage `json:"input_schema"`
	PromptSnippet    string          `json:"-"`
	PromptGuidelines []string        `json:"-"`
}

// Model identifies a provider model and the capabilities relevant to AICE.
// Connection details and credentials belong to provider configuration, not here.
type Model struct {
	ID               string     `json:"id"`
	Name             string     `json:"name,omitempty"`
	API              API        `json:"api"`
	Provider         ProviderID `json:"provider"`
	SupportsThinking bool       `json:"supports_thinking,omitempty"`
	// ThinkingLevelMap declares the reasoning levels this model supports and
	// their provider wire tokens. A nil map means the standard levels off
	// through high apply.
	ThinkingLevelMap ThinkingLevelMap `json:"thinking_level_map,omitempty"`
	// ThinkingFormat is the wire shape for thinking controls on Chat
	// Completions gateways. The empty value means the standard
	// reasoning_effort field.
	ThinkingFormat ThinkingFormat `json:"thinking_format,omitempty"`
	// SupportsReasoningEffort reports whether the gateway accepts
	// reasoning_effort alongside a non-standard ThinkingFormat. It has no
	// effect on the standard format, which always sends the field.
	SupportsReasoningEffort bool            `json:"supports_reasoning_effort,omitempty"`
	InputModalities         []InputModality `json:"input_modalities,omitempty"`
	ContextWindow           int64           `json:"context_window,omitempty"`
	MaxTokens               int64           `json:"max_tokens,omitempty"`
	Pricing                 Pricing         `json:"pricing"`
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

// CompactionSummaryMessage is a derived checkpoint stored in transcript
// context without replacing the original session turns.
type CompactionSummaryMessage struct {
	Role         Role   `json:"role"`
	Summary      string `json:"summary"`
	TokensBefore int64  `json:"tokens_before"`
	Timestamp    int64  `json:"timestamp"`
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

// MessageRole returns the transcript discriminator.
func (m UserMessage) MessageRole() Role { return m.Role }

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

// MessageRole returns the transcript discriminator.
func (m AssistantMessage) MessageRole() Role { return m.Role }

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

// MessageRole returns the transcript discriminator.
func (m ToolResultMessage) MessageRole() Role { return m.Role }

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

// NewCompactionSummaryMessage constructs one derived transcript checkpoint.
func NewCompactionSummaryMessage(
	summary string,
	tokensBefore int64,
) (CompactionSummaryMessage, error) {
	message := CompactionSummaryMessage{
		Role:         RoleCompactionSummary,
		Summary:      summary,
		TokensBefore: tokensBefore,
		Timestamp:    time.Now().UnixMilli(),
	}
	if err := message.Validate(); err != nil {
		return CompactionSummaryMessage{}, err
	}
	return message, nil
}

// MessageRole returns the transcript discriminator.
func (m CompactionSummaryMessage) MessageRole() Role { return m.Role }

// Validate checks that a compaction checkpoint contains useful derived state.
func (m CompactionSummaryMessage) Validate() error {
	if m.Role != RoleCompactionSummary {
		return fmt.Errorf("llm: compaction summary has role %q", m.Role)
	}
	if strings.TrimSpace(m.Summary) == "" {
		return fmt.Errorf("llm: compaction summary is required")
	}
	if m.TokensBefore <= 0 {
		return fmt.Errorf("llm: compaction tokens before must be positive")
	}
	return nil
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

func validateAgentMessage(message AgentMessage) error {
	switch value := message.(type) {
	case UserMessage:
		return value.Validate()
	case AssistantMessage:
		return value.Validate()
	case ToolResultMessage:
		return value.Validate()
	case CompactionSummaryMessage:
		return value.Validate()
	case nil:
		return fmt.Errorf("llm: agent message is nil")
	default:
		return fmt.Errorf("llm: unsupported agent message type %T", message)
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

func unmarshalAgentMessage(data []byte) (AgentMessage, error) {
	var envelope struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode role: %w", err)
	}

	switch envelope.Role {
	case RoleCompactionSummary:
		var message CompactionSummaryMessage
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		return message, nil
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

const (
	compactionSummaryPrefix = "The conversation history before this point was compacted " +
		"into the following summary:\n\n<summary>\n"
	compactionSummarySuffix = "\n</summary>"
)

// AgentMessagesToMessages projects transcript-only messages into standard LLM
// messages. Failed assistant attempts and their paired tool results remain in
// lossless transcript history but are not replayed to a model.
func AgentMessagesToMessages(messages []AgentMessage) ([]Message, error) {
	projected := make([]Message, 0, len(messages))
	failedToolCalls := make(map[string]struct{})
	for index, message := range messages {
		if err := validateAgentMessage(message); err != nil {
			return nil, fmt.Errorf("llm: project agent message %d: %w", index, err)
		}
		switch value := message.(type) {
		case UserMessage:
			projected = append(projected, value)
		case AssistantMessage:
			if (value.StopReason == StopReasonError || value.StopReason == StopReasonAborted) &&
				failedAssistantIsRetry(messages, index) {
				for _, part := range value.Content {
					if part.Type == ContentTypeToolCall && part.ToolCall != nil {
						failedToolCalls[part.ToolCall.ID] = struct{}{}
					}
				}
				continue
			}
			projected = append(projected, value)
		case ToolResultMessage:
			if _, failed := failedToolCalls[value.ToolCallID]; failed {
				delete(failedToolCalls, value.ToolCallID)
				continue
			}
			projected = append(projected, value)
		case CompactionSummaryMessage:
			projected = append(projected, UserMessage{
				Role: RoleUser,
				Content: []ContentPart{NewTextContent(
					compactionSummaryPrefix + value.Summary + compactionSummarySuffix,
				).Part()},
				Timestamp: value.Timestamp,
			})
		default:
			return nil, fmt.Errorf(
				"llm: project agent message %d: unsupported type %T",
				index,
				message,
			)
		}
	}
	return projected, nil
}

func failedAssistantIsRetry(messages []AgentMessage, index int) bool {
	for next := index + 1; next < len(messages); next++ {
		switch messages[next].(type) {
		case AssistantMessage:
			return true
		case UserMessage, CompactionSummaryMessage:
			return false
		}
	}
	return false
}

// MarshalAgentMessages validates and encodes complete transcript messages.
func MarshalAgentMessages(messages []AgentMessage) ([]byte, error) {
	for index, message := range messages {
		if err := validateAgentMessage(message); err != nil {
			return nil, fmt.Errorf("llm: encode agent message %d: %w", index, err)
		}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("llm: encode agent messages: %w", err)
	}
	return data, nil
}

// UnmarshalAgentMessages restores, validates, and returns concrete transcript messages.
func UnmarshalAgentMessages(data []byte) ([]AgentMessage, error) {
	var encoded []json.RawMessage
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil, fmt.Errorf("llm: decode agent messages: %w", err)
	}

	messages := make([]AgentMessage, len(encoded))
	for index, item := range encoded {
		message, err := unmarshalAgentMessage(item)
		if err != nil {
			return nil, fmt.Errorf("llm: decode agent message %d: %w", index, err)
		}
		if err := validateAgentMessage(message); err != nil {
			return nil, fmt.Errorf("llm: decode agent message %d: %w", index, err)
		}
		messages[index] = message
	}
	return messages, nil
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
	if err := r.Model.Pricing.validate(); err != nil {
		return err
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
		if err := tool.Validate(); err != nil {
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

// Validate checks tool definition invariants needed for request validation.
func (t ToolDefinition) Validate() error {
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
