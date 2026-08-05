// Package openaicompletions translates between AICE's provider-neutral LLM
// contracts and the OpenAI Chat Completions wire protocol.
//
// Chat Completions is the OpenAI-compatible protocol exposed by third-party
// model gateways such as OpenCode Go. Streaming deltas arrive as SSE
// chat.completion.chunk events; reasoning models surface thinking through the
// non-standard "reasoning_content" delta field, which the official SDK's typed
// delta struct does not model. The SDK handles transport and SSE framing; this
// adapter decodes each chunk payload itself so the thinking field is not lost.
package openaicompletions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/shared"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// API identifies the OpenAI Chat Completions wire protocol.
const API llm.API = "openai-completions"

// Config contains transport settings resolved by a provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Adapter implements AICE's model stream contract with the official OpenAI Go
// SDK's Chat Completions service. It constructs the service directly so
// unrelated OPENAI_* environment variables cannot override another provider's
// explicit settings.
type Adapter struct {
	service openaisdk.ChatCompletionService
}

// New constructs an OpenAI Chat Completions adapter.
func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("openai completions: API key is required")
	}
	if err := validateBaseURL(config.BaseURL); err != nil {
		return nil, err
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(config.BaseURL),
		option.WithMaxRetries(0),
	}
	if config.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(config.HTTPClient))
	}

	return &Adapter{
		service: openaisdk.NewChatService(opts...).Completions,
	}, nil
}

func validateBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("openai completions: parse base URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf(
			"openai completions: base URL scheme %q is not HTTP(S)",
			parsed.Scheme,
		)
	}
	if parsed.Host == "" {
		return errors.New("openai completions: base URL must include a host")
	}
	return nil
}

// Stream starts one model request. The returned stream owns the SDK response
// body and must be closed by the caller.
func (a *Adapter) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if ctx == nil {
		return nil, errors.New("openai completions: nil context")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("openai completions: validate request: %w", err)
	}

	params, err := requestParams(request)
	if err != nil {
		return nil, err
	}

	source := a.service.NewStreaming(ctx, params)
	if err := source.Err(); err != nil {
		return nil, fmt.Errorf(
			"openai completions: start response stream: %w",
			normalizeProviderError(err),
		)
	}

	return &stream{
		source:    source,
		parts:     make([]*partState, 0, 4),
		toolCalls: make(map[int64]*partState),
		message:   llm.NewAssistantMessage(request.Model),
		pricing:   request.Model.Pricing,
	}, nil
}

func requestParams(request llm.Request) (openaisdk.ChatCompletionNewParams, error) {
	if request.Model.API != API {
		return openaisdk.ChatCompletionNewParams{}, fmt.Errorf(
			"openai completions: model API %q does not match %q",
			request.Model.API,
			API,
		)
	}

	maxTokens := request.Options.MaxTokens
	if maxTokens == 0 {
		maxTokens = request.Model.MaxTokens
	}
	if maxTokens <= 0 {
		return openaisdk.ChatCompletionNewParams{}, errors.New(
			"openai completions: max output tokens must be positive",
		)
	}
	if request.Options.Temperature != nil && *request.Options.Temperature > 2 {
		return openaisdk.ChatCompletionNewParams{}, errors.New(
			"openai completions: temperature cannot exceed 2",
		)
	}

	messages, err := messageParams(request.Messages)
	if err != nil {
		return openaisdk.ChatCompletionNewParams{}, err
	}
	tools, err := toolParams(request.Tools)
	if err != nil {
		return openaisdk.ChatCompletionNewParams{}, err
	}

	params := openaisdk.ChatCompletionNewParams{
		Messages:  messages,
		Model:     shared.ChatModel(request.Model.ID),
		MaxTokens: param.NewOpt(maxTokens),
		StreamOptions: openaisdk.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if request.SystemPrompt != "" {
		params.Messages = append(
			[]openaisdk.ChatCompletionMessageParamUnion{
				openaisdk.SystemMessage(request.SystemPrompt),
			},
			messages...,
		)
	}
	if request.Options.Temperature != nil {
		params.Temperature = param.NewOpt(*request.Options.Temperature)
	}
	effort, err := reasoningEffort(request.Options.Thinking)
	if err != nil {
		return openaisdk.ChatCompletionNewParams{}, err
	}
	if effort != "" {
		params.ReasoningEffort = effort
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	return params, nil
}

// reasoningEffort maps AICE's thinking level to the Chat Completions
// reasoning_effort field. The unknown level sends nothing so the gateway keeps
// its own default.
func reasoningEffort(level llm.ThinkingLevel) (shared.ReasoningEffort, error) {
	switch level {
	case llm.ThinkingLevelUnknown:
		return "", nil
	case llm.ThinkingLevelOff:
		return shared.ReasoningEffortNone, nil
	case llm.ThinkingLevelMinimal:
		return shared.ReasoningEffortMinimal, nil
	case llm.ThinkingLevelLow:
		return shared.ReasoningEffortLow, nil
	case llm.ThinkingLevelMedium:
		return shared.ReasoningEffortMedium, nil
	case llm.ThinkingLevelHigh:
		return shared.ReasoningEffortHigh, nil
	case llm.ThinkingLevelXHigh:
		return shared.ReasoningEffortXhigh, nil
	case llm.ThinkingLevelMax:
		return shared.ReasoningEffortMax, nil
	default:
		return "", fmt.Errorf(
			"openai completions: unsupported thinking level %q",
			level,
		)
	}
}

func messageParams(
	messages []llm.Message,
) ([]openaisdk.ChatCompletionMessageParamUnion, error) {
	result := make([]openaisdk.ChatCompletionMessageParamUnion, 0, len(messages))
	for messageIndex, message := range messages {
		converted, err := messageParam(message)
		if err != nil {
			return nil, fmt.Errorf(
				"openai completions: message %d: %w",
				messageIndex,
				err,
			)
		}
		result = append(result, converted)
	}
	if len(result) == 0 {
		return nil, errors.New("openai completions: at least one message is required")
	}
	return result, nil
}

func messageParam(
	message llm.Message,
) (openaisdk.ChatCompletionMessageParamUnion, error) {
	switch value := message.(type) {
	case llm.UserMessage:
		return userMessageParam(value.Content)
	case llm.AssistantMessage:
		return assistantMessageParam(value)
	case llm.ToolResultMessage:
		return toolResultMessageParam(value)
	case nil:
		return openaisdk.ChatCompletionMessageParamUnion{}, errors.New(
			"openai completions: message is nil",
		)
	default:
		return openaisdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
			"openai completions: unsupported message type %T",
			message,
		)
	}
}

func userMessageParam(
	content []llm.ContentPart,
) (openaisdk.ChatCompletionMessageParamUnion, error) {
	if len(content) == 0 {
		return openaisdk.ChatCompletionMessageParamUnion{}, errors.New(
			"openai completions: user message content is required",
		)
	}

	hasImage := false
	for _, part := range content {
		if part.Type == llm.ContentTypeImage {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return openaisdk.UserMessage(joinText(content)), nil
	}

	parts := make([]openaisdk.ChatCompletionContentPartUnionParam, 0, len(content))
	for index, part := range content {
		switch part.Type {
		case llm.ContentTypeText:
			parts = append(parts, openaisdk.TextContentPart(part.Text))
		case llm.ContentTypeImage:
			if part.Image == nil {
				return openaisdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
					"openai completions: content %d: image payload is required",
					index,
				)
			}
			parts = append(parts, openaisdk.ImageContentPart(
				openaisdk.ChatCompletionContentPartImageImageURLParam{
					URL:    imageDataURL(*part.Image),
					Detail: "auto",
				},
			))
		default:
			return openaisdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
				"openai completions: content %d: unsupported user content type %q",
				index,
				part.Type,
			)
		}
	}
	return openaisdk.UserMessage(parts), nil
}

func assistantMessageParam(
	message llm.AssistantMessage,
) (openaisdk.ChatCompletionMessageParamUnion, error) {
	// Chat Completions has no standard way to replay prior thinking. Fold
	// thinking into visible text so both same-model and cross-model history
	// stays readable, matching how the Responses adapter projects thinking for
	// a different model.
	text := assistantText(message.Content)

	var toolCalls []openaisdk.ChatCompletionMessageToolCallUnionParam
	for index, part := range message.Content {
		switch part.Type {
		case llm.ContentTypeText, llm.ContentTypeThinking:
			// Folded into text above.
		case llm.ContentTypeToolCall:
			if part.ToolCall == nil {
				return openaisdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
					"openai completions: content %d: tool call payload is required",
					index,
				)
			}
			call := part.ToolCall
			if !json.Valid(call.Arguments) {
				return openaisdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
					"openai completions: content %d: tool call arguments are not valid JSON",
					index,
				)
			}
			toolCalls = append(toolCalls, openaisdk.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openaisdk.ChatCompletionMessageFunctionToolCallParam{
					ID: call.ID,
					Function: openaisdk.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      call.Name,
						Arguments: string(call.Arguments),
					},
				},
			})
		default:
			return openaisdk.ChatCompletionMessageParamUnion{}, fmt.Errorf(
				"openai completions: content %d: unsupported assistant content type %q",
				index,
				part.Type,
			)
		}
	}

	if len(toolCalls) == 0 {
		return openaisdk.AssistantMessage(text), nil
	}

	assistant := openaisdk.ChatCompletionAssistantMessageParam{}
	if text != "" {
		assistant.Content.OfString = param.NewOpt(text)
	}
	assistant.ToolCalls = toolCalls
	return openaisdk.ChatCompletionMessageParamUnion{OfAssistant: &assistant}, nil
}

func toolResultMessageParam(
	message llm.ToolResultMessage,
) (openaisdk.ChatCompletionMessageParamUnion, error) {
	return openaisdk.ToolMessage(joinText(message.Content), message.ToolCallID), nil
}

func assistantText(content []llm.ContentPart) string {
	var builder strings.Builder
	for _, part := range content {
		switch part.Type {
		case llm.ContentTypeText, llm.ContentTypeThinking:
			if part.Text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func joinText(content []llm.ContentPart) string {
	var builder strings.Builder
	for _, part := range content {
		if part.Type != llm.ContentTypeText || part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func toolParams(
	tools []llm.ToolDefinition,
) ([]openaisdk.ChatCompletionToolUnionParam, error) {
	result := make([]openaisdk.ChatCompletionToolUnionParam, 0, len(tools))
	for index, tool := range tools {
		if tool.Name == "" {
			return nil, fmt.Errorf("openai completions: tool %d name is required", index)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			return nil, fmt.Errorf(
				"openai completions: tool %q input schema: %w",
				tool.Name,
				err,
			)
		}
		function := shared.FunctionDefinitionParam{
			Name:       tool.Name,
			Parameters: schema,
		}
		if tool.Description != "" {
			function.Description = param.NewOpt(tool.Description)
		}
		result = append(result, openaisdk.ChatCompletionToolUnionParam{
			OfFunction: &openaisdk.ChatCompletionFunctionToolParam{
				Function: function,
			},
		})
	}
	return result, nil
}

func imageDataURL(image llm.ImageContent) string {
	return "data:" + image.MIMEType + ";base64," +
		base64.StdEncoding.EncodeToString(image.Data)
}

// partState accumulates one content part (thinking, text, or one tool call)
// streamed across chat.completions chunks.
type partState struct {
	type_        llm.ContentType
	contentIndex int
	text         strings.Builder
	toolCall     llm.ToolCall
	arguments    string
}

// wireChunk is the chat.completion.chunk SSE payload decoded from raw JSON so
// the reasoning_content delta field survives even though the SDK does not model
// it. Streamed chunks carry usage as null; the final chunk (enabled by
// stream_options.include_usage) carries the totals with empty choices.
type wireChunk struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage"`
}

type wireChoice struct {
	Delta        wireDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type wireDelta struct {
	Content          *string        `json:"content"`
	ReasoningContent *string        `json:"reasoning_content"`
	Refusal          *string        `json:"refusal"`
	ToolCalls        []wireToolCall `json:"tool_calls"`
}

type wireToolCall struct {
	Index    *int64               `json:"index"`
	ID       string               `json:"id"`
	Function wireToolCallFunction `json:"function"`
}

type wireToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireUsage struct {
	PromptTokens            int64                       `json:"prompt_tokens"`
	CompletionTokens        int64                       `json:"completion_tokens"`
	TotalTokens             int64                       `json:"total_tokens"`
	CompletionTokensDetails wireCompletionTokensDetails `json:"completion_tokens_details"`
	PromptTokensDetails     wirePromptTokensDetails     `json:"prompt_tokens_details"`
}

type wireCompletionTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type wirePromptTokensDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

func decodeWireChunk(raw string) (wireChunk, error) {
	if strings.TrimSpace(raw) == "" {
		return wireChunk{}, errors.New("empty chunk payload")
	}
	var chunk wireChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		return wireChunk{}, err
	}
	return chunk, nil
}

type stream struct {
	source       *ssestream.Stream[openaisdk.ChatCompletionChunk]
	parts        []*partState
	thinking     *partState
	text         *partState
	toolCalls    map[int64]*partState
	pending      []llm.Event
	message      llm.AssistantMessage
	pricing      llm.Pricing
	usage        llm.Usage
	nextIndex    int
	finishReason string
	started      bool
	sawRefusal   bool
	sawToolCall  bool
	finished     bool
	closed       bool
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
		wrapped := fmt.Errorf(
			"openai completions: read response stream: %w",
			normalizeProviderError(err),
		)
		message := "openai completions: model stream failed"
		if errors.Is(err, context.Canceled) {
			message = "openai completions: request canceled"
		}
		return s.errorEvent(wrapped, message), nil
	}

	events, err := s.finish()
	if err != nil {
		return s.errorEvent(err, err.Error()), nil
	}
	if len(events) == 0 {
		return llm.Event{}, io.EOF
	}
	s.pending = events
	return s.shift(), nil
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
		return fmt.Errorf("openai completions: close response stream: %w", err)
	}
	return nil
}

func (s *stream) translate(chunk openaisdk.ChatCompletionChunk) ([]llm.Event, error) {
	var events []llm.Event
	if !s.started {
		s.started = true
		events = append(events, llm.Event{Type: llm.EventTypeStart})
	}

	if chunk.ID != "" {
		s.message.ResponseID = chunk.ID
	}
	if chunk.Model != "" && chunk.Model != s.message.ModelID {
		s.message.ResponseModelID = chunk.Model
	}

	wire, err := decodeWireChunk(chunk.RawJSON())
	if err != nil {
		return nil, fmt.Errorf("openai completions: decode chunk: %w", err)
	}

	for _, choice := range wire.Choices {
		events = append(events, s.deltaEvents(choice.Delta)...)
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finishReason = *choice.FinishReason
		}
	}
	s.mergeUsage(wire.Usage)
	return events, nil
}

func (s *stream) deltaEvents(delta wireDelta) []llm.Event {
	var events []llm.Event
	if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
		events = append(events, s.thinkingDelta(*delta.ReasoningContent)...)
	}
	if delta.Content != nil && *delta.Content != "" {
		events = append(events, s.textDelta(*delta.Content)...)
	}
	if delta.Refusal != nil && *delta.Refusal != "" {
		s.sawRefusal = true
		events = append(events, s.textDelta(*delta.Refusal)...)
	}
	for _, call := range delta.ToolCalls {
		events = append(events, s.toolCallDelta(call)...)
	}
	return events
}

func (s *stream) thinkingDelta(delta string) []llm.Event {
	if s.thinking == nil {
		state := &partState{type_: llm.ContentTypeThinking, contentIndex: s.nextIndex}
		s.nextIndex++
		s.thinking = state
		s.parts = append(s.parts, state)
		state.text.WriteString(delta)
		return []llm.Event{
			{Type: llm.EventTypeThinkingStart, ContentIndex: state.contentIndex},
			{Type: llm.EventTypeThinkingDelta, ContentIndex: state.contentIndex, Delta: delta},
		}
	}
	state := s.thinking
	state.text.WriteString(delta)
	return []llm.Event{{
		Type:         llm.EventTypeThinkingDelta,
		ContentIndex: state.contentIndex,
		Delta:        delta,
	}}
}

func (s *stream) textDelta(delta string) []llm.Event {
	if s.text == nil {
		state := &partState{type_: llm.ContentTypeText, contentIndex: s.nextIndex}
		s.nextIndex++
		s.text = state
		s.parts = append(s.parts, state)
		state.text.WriteString(delta)
		return []llm.Event{
			{Type: llm.EventTypeTextStart, ContentIndex: state.contentIndex},
			{Type: llm.EventTypeTextDelta, ContentIndex: state.contentIndex, Delta: delta},
		}
	}
	state := s.text
	state.text.WriteString(delta)
	return []llm.Event{{
		Type:         llm.EventTypeTextDelta,
		ContentIndex: state.contentIndex,
		Delta:        delta,
	}}
}

func (s *stream) toolCallDelta(call wireToolCall) []llm.Event {
	index := int64(len(s.toolCalls))
	if call.Index != nil {
		index = *call.Index
	}

	state, exists := s.toolCalls[index]
	if !exists {
		state = &partState{type_: llm.ContentTypeToolCall, contentIndex: s.nextIndex}
		s.nextIndex++
		state.toolCall.ID = call.ID
		state.toolCall.Name = call.Function.Name
		s.toolCalls[index] = state
		s.parts = append(s.parts, state)
		s.sawToolCall = true

		start := llm.Event{
			Type:         llm.EventTypeToolCallStart,
			ContentIndex: state.contentIndex,
		}
		if state.toolCall.ID != "" || state.toolCall.Name != "" {
			start.ToolCallDelta = &llm.ToolCallDelta{
				ID:   state.toolCall.ID,
				Name: state.toolCall.Name,
			}
		}
		events := []llm.Event{start}
		if call.Function.Arguments != "" {
			state.arguments += call.Function.Arguments
			events = append(events, llm.Event{
				Type:         llm.EventTypeToolCallDelta,
				ContentIndex: state.contentIndex,
				ToolCallDelta: &llm.ToolCallDelta{
					ArgumentsDelta: call.Function.Arguments,
				},
			})
		}
		return events
	}

	if call.ID != "" {
		state.toolCall.ID = call.ID
	}
	if call.Function.Name != "" {
		state.toolCall.Name = call.Function.Name
	}
	if call.Function.Arguments == "" {
		return nil
	}
	state.arguments += call.Function.Arguments
	return []llm.Event{{
		Type:         llm.EventTypeToolCallDelta,
		ContentIndex: state.contentIndex,
		ToolCallDelta: &llm.ToolCallDelta{
			ArgumentsDelta: call.Function.Arguments,
		},
	}}
}

func (s *stream) mergeUsage(usage *wireUsage) {
	if usage == nil {
		return
	}
	cacheRead := usage.PromptTokensDetails.CachedTokens
	cacheWrite := usage.PromptTokensDetails.CacheWriteTokens
	input := max(usage.PromptTokens-cacheRead-cacheWrite, 0)
	s.usage = llm.Usage{
		InputTokens:      input,
		OutputTokens:     usage.CompletionTokens,
		ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		TotalTokens:      usage.TotalTokens,
	}
	s.usage.Cost = llm.EstimateCost(s.pricing, s.usage)
}

// finish assembles the terminal events once the SSE stream ends. Tool calls
// must have valid JSON arguments; a truncated call is a protocol error and the
// agent loop will surface it instead of executing a partial call.
func (s *stream) finish() ([]llm.Event, error) {
	for _, state := range s.toolCalls {
		if !json.Valid([]byte(toolCallArguments(state))) {
			return nil, fmt.Errorf(
				"openai completions: tool call %q ended with invalid JSON arguments",
				state.toolCall.Name,
			)
		}
	}

	s.finished = true
	reason := stopReason(s.finishReason)
	if reason == llm.StopReasonStop {
		switch {
		case s.sawToolCall:
			reason = llm.StopReasonToolUse
		case s.sawRefusal:
			reason = llm.StopReasonRefusal
		}
	}

	events := make([]llm.Event, 0, len(s.parts)+3)
	for _, state := range s.parts {
		events = append(events, s.endEvent(state))
	}
	usage := s.usage
	message := s.messageSnapshot(reason, "")
	return append(events,
		llm.Event{Type: llm.EventTypeUsage, Usage: &usage},
		llm.Event{Type: llm.EventTypeDone, StopReason: reason, Message: &message},
	), nil
}

func stopReason(finishReason string) llm.StopReason {
	switch finishReason {
	case "", "stop":
		return llm.StopReasonStop
	case "length":
		return llm.StopReasonLength
	case "tool_calls", "function_call":
		return llm.StopReasonToolUse
	case "content_filter":
		return llm.StopReasonRefusal
	default:
		return llm.StopReasonStop
	}
}

func (s *stream) endEvent(state *partState) llm.Event {
	switch state.type_ {
	case llm.ContentTypeText:
		content := llm.NewTextContent(state.text.String()).Part()
		return llm.Event{
			Type:         llm.EventTypeTextEnd,
			ContentIndex: state.contentIndex,
			Content:      &content,
		}
	case llm.ContentTypeThinking:
		content := llm.NewThinkingContent(state.text.String(), "").Part()
		return llm.Event{
			Type:         llm.EventTypeThinkingEnd,
			ContentIndex: state.contentIndex,
			Content:      &content,
		}
	case llm.ContentTypeToolCall:
		call := state.toolCall
		call.Arguments = append(json.RawMessage(nil), toolCallArguments(state)...)
		content := llm.ContentPart{Type: llm.ContentTypeToolCall, ToolCall: &call}
		return llm.Event{
			Type:         llm.EventTypeToolCallEnd,
			ContentIndex: state.contentIndex,
			Content:      &content,
			ToolCall:     &call,
		}
	default:
		return llm.Event{Type: llm.EventTypeUnknown, ContentIndex: state.contentIndex}
	}
}

// toolCallArguments returns the accumulated arguments for a streamed tool call,
// normalizing an absent/empty payload to an empty object so a no-argument tool
// call still produces valid JSON.
func toolCallArguments(state *partState) string {
	if strings.TrimSpace(state.arguments) == "" {
		return "{}"
	}
	return state.arguments
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

func (s *stream) messageSnapshot(
	reason llm.StopReason,
	errorMessage string,
) llm.AssistantMessage {
	message := s.message
	message.Content = s.contentSnapshot()
	message.Usage = s.usage
	message.StopReason = reason
	message.ErrorMessage = errorMessage
	return message
}

func (s *stream) contentSnapshot() []llm.ContentPart {
	result := make([]llm.ContentPart, 0, len(s.parts))
	for _, state := range s.parts {
		switch state.type_ {
		case llm.ContentTypeText:
			result = append(result, llm.NewTextContent(state.text.String()).Part())
		case llm.ContentTypeThinking:
			result = append(result, llm.NewThinkingContent(state.text.String(), "").Part())
		case llm.ContentTypeToolCall:
			arguments := toolCallArguments(state)
			if !json.Valid([]byte(arguments)) {
				// Exclude a truncated call from snapshots so the assembled
				// assistant message still validates; the stream error already
				// carries the failure.
				continue
			}
			call := state.toolCall
			call.Arguments = append(json.RawMessage(nil), arguments...)
			result = append(result, llm.ContentPart{
				Type:     llm.ContentTypeToolCall,
				ToolCall: &call,
			})
		}
	}
	return result
}

func normalizeProviderError(err error) error {
	var apiErr *openaisdk.Error
	if !errors.As(err, &apiErr) {
		return llm.NewTransportProviderError(err)
	}
	var header http.Header
	if apiErr.Response != nil {
		header = apiErr.Response.Header
	}
	code := apiErr.Code
	if code == "" {
		code = apiErr.Type
	}
	return llm.NewHTTPProviderError(err, apiErr.StatusCode, code, header)
}
