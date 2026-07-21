// Package anthropic translates between AICE's provider-neutral LLM contracts
// and the Anthropic Messages protocol.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// API identifies the Anthropic Messages wire protocol.
const API llm.API = "anthropic-messages"

// Config contains transport settings resolved by a provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Adapter implements AICE's model stream contract with the official Anthropic
// SDK. Provider credentials and defaults are passed explicitly so Anthropic
// environment variables cannot accidentally override another provider.
type Adapter struct {
	client anthropicsdk.Client
}

// New constructs an Anthropic Messages adapter.
func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("anthropic: API key is required")
	}
	if err := validateBaseURL(config.BaseURL); err != nil {
		return nil, err
	}

	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(config.BaseURL),
		option.WithMaxRetries(0),
	}
	if config.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(config.HTTPClient))
	}

	return &Adapter{client: anthropicsdk.NewClient(opts...)}, nil
}

func validateBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("anthropic: parse base URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("anthropic: base URL scheme %q is not HTTP(S)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("anthropic: base URL must include a host")
	}
	return nil
}

// Stream starts one model request. The returned stream owns the SDK response
// body and must be closed by the caller.
func (a *Adapter) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if ctx == nil {
		return nil, errors.New("anthropic: nil context")
	}

	params, err := requestParams(request)
	if err != nil {
		return nil, err
	}

	source := a.client.Messages.NewStreaming(ctx, params)
	if err := source.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: start message stream: %w", err)
	}

	return &stream{
		source:   source,
		blocks:   make(map[int]*blockState),
		contents: make(map[int]llm.ContentPart),
		message:  llm.NewAssistantMessage(request.Model),
	}, nil
}

func requestParams(request llm.Request) (anthropicsdk.MessageNewParams, error) {
	if request.Model.API != API {
		return anthropicsdk.MessageNewParams{}, fmt.Errorf(
			"anthropic: model API %q does not match %q",
			request.Model.API,
			API,
		)
	}
	if request.Model.ID == "" {
		return anthropicsdk.MessageNewParams{}, errors.New("anthropic: model ID is required")
	}
	if request.Model.Provider == "" {
		return anthropicsdk.MessageNewParams{}, errors.New("anthropic: model provider is required")
	}

	maxTokens := request.Options.MaxTokens
	if maxTokens == 0 {
		maxTokens = request.Model.MaxTokens
	}
	if maxTokens <= 0 {
		return anthropicsdk.MessageNewParams{}, errors.New("anthropic: max tokens must be positive")
	}

	messages, err := messageParams(request.Messages)
	if err != nil {
		return anthropicsdk.MessageNewParams{}, err
	}
	if len(messages) == 0 {
		return anthropicsdk.MessageNewParams{}, errors.New("anthropic: at least one message is required")
	}

	tools, err := toolParams(request.Tools)
	if err != nil {
		return anthropicsdk.MessageNewParams{}, err
	}

	params := anthropicsdk.MessageNewParams{
		MaxTokens: maxTokens,
		Messages:  messages,
		Model:     anthropicsdk.Model(request.Model.ID),
		Tools:     tools,
	}
	if request.SystemPrompt != "" {
		params.System = []anthropicsdk.TextBlockParam{{Text: request.SystemPrompt}}
	}
	if request.Options.Temperature != nil {
		params.Temperature = anthropicsdk.Float(*request.Options.Temperature)
	}
	if err := applyThinking(&params, request.Options.Thinking); err != nil {
		return anthropicsdk.MessageNewParams{}, err
	}

	return params, nil
}

func applyThinking(params *anthropicsdk.MessageNewParams, level llm.ThinkingLevel) error {
	switch level {
	case llm.ThinkingLevelUnknown:
		return nil
	case llm.ThinkingLevelOff:
		disabled := anthropicsdk.NewThinkingConfigDisabledParam()
		params.Thinking.OfDisabled = &disabled
		return nil
	case llm.ThinkingLevelMinimal, llm.ThinkingLevelLow:
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortLow
	case llm.ThinkingLevelMedium:
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortMedium
	case llm.ThinkingLevelHigh:
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortHigh
	case llm.ThinkingLevelXHigh:
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortXhigh
	case llm.ThinkingLevelMax:
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortMax
	default:
		return fmt.Errorf("anthropic: unsupported thinking level %q", level)
	}
	return nil
}

func messageParams(messages []llm.Message) ([]anthropicsdk.MessageParam, error) {
	result := make([]anthropicsdk.MessageParam, 0, len(messages))
	for messageIndex, message := range messages {
		if err := message.Validate(); err != nil {
			return nil, fmt.Errorf("anthropic: message %d: %w", messageIndex, err)
		}

		blocks := make([]anthropicsdk.ContentBlockParamUnion, 0, len(message.Content))
		for partIndex, part := range message.Content {
			block, err := contentBlockParam(message.Role, part)
			if err != nil {
				return nil, fmt.Errorf(
					"anthropic: message %d content %d: %w",
					messageIndex,
					partIndex,
					err,
				)
			}
			blocks = append(blocks, block)
		}

		switch message.Role {
		case llm.RoleUser, llm.RoleTool:
			result = append(result, anthropicsdk.NewUserMessage(blocks...))
		case llm.RoleAssistant:
			result = append(result, anthropicsdk.NewAssistantMessage(blocks...))
		default:
			return nil, fmt.Errorf("anthropic: message %d has unsupported role %q", messageIndex, message.Role)
		}
	}
	return result, nil
}

func contentBlockParam(role llm.Role, part llm.ContentPart) (anthropicsdk.ContentBlockParamUnion, error) {
	switch part.Type {
	case llm.ContentTypeText:
		return anthropicsdk.NewTextBlock(part.Text), nil
	case llm.ContentTypeThinking:
		if role != llm.RoleAssistant {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("thinking content requires an assistant role")
		}
		if part.Redacted {
			return anthropicsdk.NewRedactedThinkingBlock(part.Text), nil
		}
		return anthropicsdk.NewThinkingBlock(part.Signature, part.Text), nil
	case llm.ContentTypeImage:
		if part.Image == nil {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("image payload is required")
		}
		return anthropicsdk.NewImageBlockBase64(
			part.Image.MIMEType,
			base64.StdEncoding.EncodeToString(part.Image.Data),
		), nil
	case llm.ContentTypeToolCall:
		if role != llm.RoleAssistant {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("tool call requires an assistant role")
		}
		if part.ToolCall == nil {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("tool call payload is required")
		}
		if !json.Valid(part.ToolCall.Arguments) {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("tool call arguments are not valid JSON")
		}
		return anthropicsdk.NewToolUseBlock(
			part.ToolCall.ID,
			part.ToolCall.Arguments,
			part.ToolCall.Name,
		), nil
	case llm.ContentTypeToolResult:
		if role != llm.RoleUser && role != llm.RoleTool {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("tool result requires a user or tool role")
		}
		return toolResultBlockParam(part.ToolResult)
	default:
		return anthropicsdk.ContentBlockParamUnion{}, fmt.Errorf("unsupported content type %q", part.Type)
	}
}

func toolResultBlockParam(result *llm.ToolResult) (anthropicsdk.ContentBlockParamUnion, error) {
	if result == nil {
		return anthropicsdk.ContentBlockParamUnion{}, errors.New("tool result payload is required")
	}

	content := make([]anthropicsdk.ToolResultBlockParamContentUnion, 0, len(result.Content))
	for index, part := range result.Content {
		if part.Type != llm.ContentTypeText {
			return anthropicsdk.ContentBlockParamUnion{}, fmt.Errorf(
				"tool result content %d has unsupported type %q",
				index,
				part.Type,
			)
		}
		content = append(content, anthropicsdk.ToolResultBlockParamContentUnion{
			OfText: &anthropicsdk.TextBlockParam{Text: part.Text},
		})
	}
	if len(content) == 0 {
		content = append(content, anthropicsdk.ToolResultBlockParamContentUnion{
			OfText: &anthropicsdk.TextBlockParam{},
		})
	}

	return anthropicsdk.ContentBlockParamUnion{
		OfToolResult: &anthropicsdk.ToolResultBlockParam{
			ToolUseID: result.CallID,
			Content:   content,
			IsError:   anthropicsdk.Bool(result.IsError),
		},
	}, nil
}

func toolParams(tools []llm.ToolDefinition) ([]anthropicsdk.ToolUnionParam, error) {
	result := make([]anthropicsdk.ToolUnionParam, 0, len(tools))
	for index, tool := range tools {
		if tool.Name == "" {
			return nil, fmt.Errorf("anthropic: tool %d name is required", index)
		}

		var schema anthropicsdk.ToolInputSchemaParam
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, fmt.Errorf("anthropic: tool %q input schema: %w", tool.Name, err)
		}

		converted := anthropicsdk.ToolUnionParamOfTool(schema, tool.Name)
		if tool.Description != "" {
			converted.OfTool.Description = anthropicsdk.String(tool.Description)
		}
		result = append(result, converted)
	}
	return result, nil
}

type blockState struct {
	type_            llm.ContentType
	text             strings.Builder
	signature        strings.Builder
	redacted         bool
	toolCall         llm.ToolCall
	initialArguments json.RawMessage
}

type stream struct {
	source   *ssestream.Stream[anthropicsdk.MessageStreamEventUnion]
	blocks   map[int]*blockState
	contents map[int]llm.ContentPart
	pending  []llm.Event
	message  llm.AssistantMessage
	usage    llm.Usage
	stop     llm.StopReason
	finished bool
	closed   bool
}

func (s *stream) Next() (llm.Event, error) {
	if len(s.pending) > 0 {
		return s.shift(), nil
	}
	if s.finished || s.closed {
		return llm.Event{}, io.EOF
	}

	for s.source.Next() {
		events, err := s.translate(s.source.Current())
		if err != nil {
			return s.errorEvent(err, err.Error()), nil
		}
		if len(events) == 0 {
			continue
		}
		s.pending = events
		return s.shift(), nil
	}

	if err := s.source.Err(); err != nil {
		wrapped := fmt.Errorf("anthropic: read message stream: %w", err)
		message := "anthropic: model stream failed"
		if errors.Is(err, context.Canceled) {
			message = "anthropic: request canceled"
		}
		return s.errorEvent(wrapped, message), nil
	}
	err := fmt.Errorf(
		"anthropic: message stream ended before message_stop: %w",
		io.ErrUnexpectedEOF,
	)
	return s.errorEvent(err, "anthropic: model stream ended unexpectedly"), nil
}

func (s *stream) shift() llm.Event {
	event := s.pending[0]
	s.pending = s.pending[1:]
	return event
}

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.source.Close(); err != nil {
		return fmt.Errorf("anthropic: close message stream: %w", err)
	}
	return nil
}

func (s *stream) translate(event anthropicsdk.MessageStreamEventUnion) ([]llm.Event, error) {
	switch value := event.AsAny().(type) {
	case anthropicsdk.MessageStartEvent:
		s.message.ResponseID = value.Message.ID
		if value.Message.Model != "" {
			s.message.ModelID = string(value.Message.Model)
		}
		s.mergeUsage(value.Message.Usage)
		return []llm.Event{{Type: llm.EventTypeStart}}, nil
	case anthropicsdk.ContentBlockStartEvent:
		return s.startBlock(int(value.Index), value.ContentBlock)
	case anthropicsdk.ContentBlockDeltaEvent:
		return s.deltaBlock(int(value.Index), value.Delta)
	case anthropicsdk.ContentBlockStopEvent:
		return s.stopBlock(int(value.Index))
	case anthropicsdk.MessageDeltaEvent:
		s.mergeDeltaUsage(value.Usage)
		s.stop = stopReason(value.Delta.StopReason)
		usage := s.usage
		return []llm.Event{{Type: llm.EventTypeUsage, Usage: &usage}}, nil
	case anthropicsdk.MessageStopEvent:
		if len(s.blocks) != 0 {
			return nil, errors.New("anthropic: message stopped with incomplete content blocks")
		}
		s.finished = true
		message := s.messageSnapshot(s.stop, "")
		return []llm.Event{{
			Type:       llm.EventTypeDone,
			StopReason: s.stop,
			Message:    &message,
		}}, nil
	default:
		return nil, fmt.Errorf("anthropic: unsupported stream event %q", event.Type)
	}
}

func (s *stream) startBlock(
	index int,
	block anthropicsdk.ContentBlockStartEventContentBlockUnion,
) ([]llm.Event, error) {
	if _, exists := s.blocks[index]; exists {
		return nil, fmt.Errorf("anthropic: content block %d started twice", index)
	}
	if _, exists := s.contents[index]; exists {
		return nil, fmt.Errorf("anthropic: content block %d started twice", index)
	}

	state := &blockState{}
	var events []llm.Event
	switch value := block.AsAny().(type) {
	case anthropicsdk.TextBlock:
		state.type_ = llm.ContentTypeText
		state.text.WriteString(value.Text)
		events = append(events, llm.Event{Type: llm.EventTypeTextStart, ContentIndex: index})
		if value.Text != "" {
			events = append(events, llm.Event{
				Type:         llm.EventTypeTextDelta,
				ContentIndex: index,
				Delta:        value.Text,
			})
		}
	case anthropicsdk.ThinkingBlock:
		state.type_ = llm.ContentTypeThinking
		state.text.WriteString(value.Thinking)
		state.signature.WriteString(value.Signature)
		events = append(events, llm.Event{Type: llm.EventTypeThinkingStart, ContentIndex: index})
		if value.Thinking != "" {
			events = append(events, llm.Event{
				Type:         llm.EventTypeThinkingDelta,
				ContentIndex: index,
				Delta:        value.Thinking,
			})
		}
	case anthropicsdk.RedactedThinkingBlock:
		state.type_ = llm.ContentTypeThinking
		state.redacted = true
		state.text.WriteString(value.Data)
		events = append(events, llm.Event{Type: llm.EventTypeThinkingStart, ContentIndex: index})
	case anthropicsdk.ToolUseBlock:
		state.type_ = llm.ContentTypeToolCall
		state.toolCall.ID = value.ID
		state.toolCall.Name = value.Name
		state.initialArguments = append(json.RawMessage(nil), value.Input...)
		events = append(events, llm.Event{
			Type:         llm.EventTypeToolCallStart,
			ContentIndex: index,
			ToolCallDelta: &llm.ToolCallDelta{
				ID:   value.ID,
				Name: value.Name,
			},
		})
	default:
		return nil, fmt.Errorf("anthropic: unsupported content block %q", block.Type)
	}

	s.blocks[index] = state
	return events, nil
}

func (s *stream) deltaBlock(
	index int,
	delta anthropicsdk.RawContentBlockDeltaUnion,
) ([]llm.Event, error) {
	state, exists := s.blocks[index]
	if !exists {
		return nil, fmt.Errorf("anthropic: delta for unknown content block %d", index)
	}

	switch value := delta.AsAny().(type) {
	case anthropicsdk.TextDelta:
		if state.type_ != llm.ContentTypeText {
			return nil, fmt.Errorf("anthropic: text delta for non-text content block %d", index)
		}
		state.text.WriteString(value.Text)
		return []llm.Event{{
			Type:         llm.EventTypeTextDelta,
			ContentIndex: index,
			Delta:        value.Text,
		}}, nil
	case anthropicsdk.ThinkingDelta:
		if state.type_ != llm.ContentTypeThinking || state.redacted {
			return nil, fmt.Errorf("anthropic: thinking delta for incompatible content block %d", index)
		}
		state.text.WriteString(value.Thinking)
		return []llm.Event{{
			Type:         llm.EventTypeThinkingDelta,
			ContentIndex: index,
			Delta:        value.Thinking,
		}}, nil
	case anthropicsdk.SignatureDelta:
		if state.type_ != llm.ContentTypeThinking || state.redacted {
			return nil, fmt.Errorf("anthropic: signature delta for incompatible content block %d", index)
		}
		state.signature.WriteString(value.Signature)
		return nil, nil
	case anthropicsdk.InputJSONDelta:
		if state.type_ != llm.ContentTypeToolCall {
			return nil, fmt.Errorf("anthropic: tool input delta for non-tool content block %d", index)
		}
		state.text.WriteString(value.PartialJSON)
		return []llm.Event{{
			Type:         llm.EventTypeToolCallDelta,
			ContentIndex: index,
			ToolCallDelta: &llm.ToolCallDelta{
				ArgumentsDelta: value.PartialJSON,
			},
		}}, nil
	default:
		return nil, fmt.Errorf("anthropic: unsupported content delta %q", delta.Type)
	}
}

func (s *stream) stopBlock(index int) ([]llm.Event, error) {
	state, exists := s.blocks[index]
	if !exists {
		return nil, fmt.Errorf("anthropic: stop for unknown content block %d", index)
	}
	delete(s.blocks, index)

	switch state.type_ {
	case llm.ContentTypeText:
		content := llm.NewTextContent(state.text.String()).Part()
		s.contents[index] = content
		return []llm.Event{{
			Type:         llm.EventTypeTextEnd,
			ContentIndex: index,
			Content:      &content,
		}}, nil
	case llm.ContentTypeThinking:
		thinking := llm.NewThinkingContent(state.text.String(), state.signature.String())
		thinking.Redacted = state.redacted
		content := thinking.Part()
		s.contents[index] = content
		return []llm.Event{{
			Type:         llm.EventTypeThinkingEnd,
			ContentIndex: index,
			Content:      &content,
		}}, nil
	case llm.ContentTypeToolCall:
		arguments := json.RawMessage(state.text.String())
		if len(arguments) == 0 {
			arguments = state.initialArguments
		}
		if !json.Valid(arguments) {
			return nil, fmt.Errorf("anthropic: tool call %q ended with invalid JSON", state.toolCall.Name)
		}
		state.toolCall.Arguments = append(json.RawMessage(nil), arguments...)
		content := llm.ContentPart{
			Type:     llm.ContentTypeToolCall,
			ToolCall: &state.toolCall,
		}
		s.contents[index] = content
		return []llm.Event{{
			Type:         llm.EventTypeToolCallEnd,
			ContentIndex: index,
			Content:      &content,
			ToolCall:     &state.toolCall,
		}}, nil
	default:
		return nil, fmt.Errorf("anthropic: content block %d has unknown type %q", index, state.type_)
	}
}

func (s *stream) errorEvent(err error, errorMessage string) llm.Event {
	s.finished = true
	reason := llm.StopReasonError
	if errors.Is(err, context.Canceled) {
		reason = llm.StopReasonAborted
	}
	message := s.messageSnapshot(reason, errorMessage)
	return llm.Event{
		Type:       llm.EventTypeError,
		StopReason: reason,
		Message:    &message,
		Err:        err,
	}
}

func (s *stream) messageSnapshot(reason llm.StopReason, errorMessage string) llm.AssistantMessage {
	message := s.message
	message.Content = s.contentSnapshot()
	message.Usage = s.usage
	message.StopReason = reason
	message.ErrorMessage = errorMessage
	return message
}

func (s *stream) contentSnapshot() []llm.ContentPart {
	contents := make(map[int]llm.ContentPart, len(s.contents)+len(s.blocks))
	for index, content := range s.contents {
		contents[index] = content
	}
	for index, state := range s.blocks {
		if content, ok := state.partialContent(); ok {
			contents[index] = content
		}
	}

	indexes := make([]int, 0, len(contents))
	for index := range contents {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	result := make([]llm.ContentPart, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, contents[index])
	}
	return result
}

func (s *blockState) partialContent() (llm.ContentPart, bool) {
	switch s.type_ {
	case llm.ContentTypeText:
		return llm.NewTextContent(s.text.String()).Part(), true
	case llm.ContentTypeThinking:
		thinking := llm.NewThinkingContent(s.text.String(), s.signature.String())
		thinking.Redacted = s.redacted
		return thinking.Part(), true
	case llm.ContentTypeToolCall:
		arguments := json.RawMessage(s.text.String())
		if len(arguments) == 0 {
			arguments = s.initialArguments
		}
		if !json.Valid(arguments) {
			return llm.ContentPart{}, false
		}
		call := s.toolCall
		call.Arguments = append(json.RawMessage(nil), arguments...)
		return llm.ContentPart{
			Type:     llm.ContentTypeToolCall,
			ToolCall: &call,
		}, true
	default:
		return llm.ContentPart{}, false
	}
}

func (s *stream) mergeUsage(usage anthropicsdk.Usage) {
	s.usage.InputTokens = usage.InputTokens
	s.usage.OutputTokens = usage.OutputTokens
	s.usage.ReasoningTokens = usage.OutputTokensDetails.ThinkingTokens
	s.usage.CacheReadTokens = usage.CacheReadInputTokens
	s.usage.CacheWriteTokens = usage.CacheCreationInputTokens
	s.updateTotalTokens()
}

func (s *stream) mergeDeltaUsage(usage anthropicsdk.MessageDeltaUsage) {
	if usage.JSON.InputTokens.Valid() {
		s.usage.InputTokens = usage.InputTokens
	}
	if usage.JSON.OutputTokens.Valid() {
		s.usage.OutputTokens = usage.OutputTokens
	}
	if usage.JSON.OutputTokensDetails.Valid() {
		s.usage.ReasoningTokens = usage.OutputTokensDetails.ThinkingTokens
	}
	if usage.JSON.CacheReadInputTokens.Valid() {
		s.usage.CacheReadTokens = usage.CacheReadInputTokens
	}
	if usage.JSON.CacheCreationInputTokens.Valid() {
		s.usage.CacheWriteTokens = usage.CacheCreationInputTokens
	}
	s.updateTotalTokens()
}

func (s *stream) updateTotalTokens() {
	s.usage.TotalTokens = s.usage.InputTokens +
		s.usage.OutputTokens +
		s.usage.CacheReadTokens +
		s.usage.CacheWriteTokens
}

func stopReason(reason anthropicsdk.StopReason) llm.StopReason {
	switch reason {
	case anthropicsdk.StopReasonEndTurn, anthropicsdk.StopReasonStopSequence:
		return llm.StopReasonStop
	case anthropicsdk.StopReasonMaxTokens:
		return llm.StopReasonLength
	case anthropicsdk.StopReasonToolUse:
		return llm.StopReasonToolUse
	case anthropicsdk.StopReasonPauseTurn:
		return llm.StopReasonPause
	case anthropicsdk.StopReasonRefusal:
		return llm.StopReasonRefusal
	default:
		return llm.StopReasonUnknown
	}
}
