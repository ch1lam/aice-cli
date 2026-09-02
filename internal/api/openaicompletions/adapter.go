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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/shared"

	"github.com/ch1lam/aice-cli/internal/api/streamcore"
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
		core:      streamcore.NewStream(request.Model),
		source:    source,
		toolCalls: make(map[int64]*partState),
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

	if err := streamcore.ValidateTemperature(request.Options.Temperature); err != nil {
		return openaisdk.ChatCompletionNewParams{}, fmt.Errorf("openai completions: %w", err)
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
		Messages: messages,
		Model:    shared.ChatModel(request.Model.ID),
		StreamOptions: openaisdk.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if !request.Model.OmitMaxTokensByDefault || request.Options.MaxTokens > 0 {
		maxTokens, err := streamcore.ResolveMaxTokens(request)
		if err != nil {
			return openaisdk.ChatCompletionNewParams{}, fmt.Errorf("openai completions: %w", err)
		}
		params.MaxTokens = param.NewOpt(maxTokens)
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
	if err := applyThinking(&params, request.Model, request.Options.Thinking); err != nil {
		return openaisdk.ChatCompletionNewParams{}, err
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	return params, nil
}

// thinkingControls contains the model-specific Chat Completions fields for a
// canonical thinking level.
type thinkingControls struct {
	reasoningEffort string
	extraFields     map[string]any
}

// applyThinking maps the requested thinking level onto the Chat Completions
// wire controls for the model. The standard format sends reasoning_effort
// (never the literal "off" token); the DeepSeek format sends a thinking
// toggle object; the Qwen format sends enable_thinking. Toggle formats only
// add reasoning_effort when the model declares support for it.
func applyThinking(
	params *openaisdk.ChatCompletionNewParams,
	model llm.Model,
	level llm.ThinkingLevel,
) error {
	controls, err := thinkingControlsFor(model, level)
	if err != nil {
		return fmt.Errorf("openai completions: %w", err)
	}
	if controls.reasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(controls.reasoningEffort)
	}
	if len(controls.extraFields) > 0 {
		params.SetExtraFields(controls.extraFields)
	}
	return nil
}

// thinkingControlsFor resolves one canonical thinking level through the
// model's capability map and then selects the protocol-specific wire shape.
// The unknown level sends nothing so the gateway keeps its own default.
func thinkingControlsFor(
	model llm.Model,
	level llm.ThinkingLevel,
) (thinkingControls, error) {
	if level == llm.ThinkingLevelUnknown {
		return thinkingControls{}, nil
	}
	effort, supported := model.ThinkingLevelMap.WireValue(level)
	if !supported {
		return thinkingControls{}, fmt.Errorf(
			"model %q does not support thinking level %q",
			model.ID,
			level,
		)
	}

	switch model.ThinkingFormat {
	case llm.ThinkingFormatDeepSeek:
		thinkingType := "enabled"
		if level == llm.ThinkingLevelOff {
			thinkingType = "disabled"
		}
		controls := thinkingControls{extraFields: map[string]any{
			"thinking": map[string]string{"type": thinkingType},
		}}
		if level != llm.ThinkingLevelOff && model.SupportsReasoningEffort {
			controls.reasoningEffort = effort
		}
		return controls, nil
	case llm.ThinkingFormatQwen:
		enabled := level != llm.ThinkingLevelOff
		controls := thinkingControls{extraFields: map[string]any{
			"enable_thinking": enabled,
		}}
		if enabled && model.SupportsReasoningEffort {
			controls.reasoningEffort = effort
		}
		return controls, nil
	case "":
		// The Chat Completions protocol has no "off" effort. A model that
		// maps off to the canonical token disables thinking by omitting the
		// field; a model that maps off to "none" sends it explicitly.
		if effort == string(llm.ThinkingLevelOff) {
			return thinkingControls{}, nil
		}
		return thinkingControls{reasoningEffort: effort}, nil
	default:
		return thinkingControls{}, fmt.Errorf(
			"model %q uses unsupported thinking format %q",
			model.ID,
			model.ThinkingFormat,
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
					URL:    streamcore.ImageDataURL(*part.Image),
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
	for _, part := range streamcore.ProjectThinkingToText(content) {
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
	schemas, err := streamcore.DecodeToolSchemas(tools)
	if err != nil {
		return nil, fmt.Errorf("openai completions: %w", err)
	}
	result := make([]openaisdk.ChatCompletionToolUnionParam, 0, len(tools))
	for index, tool := range tools {
		function := shared.FunctionDefinitionParam{
			Name:       tool.Name,
			Parameters: schemas[index],
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

func (s *partState) PartialContent() (llm.ContentPart, bool) {
	switch s.type_ {
	case llm.ContentTypeText:
		return streamcore.PartialText(s.text.String(), ""), true
	case llm.ContentTypeThinking:
		return streamcore.PartialThinking(s.text.String(), "", false), true
	case llm.ContentTypeToolCall:
		return streamcore.PartialToolCall(s.toolCall, s.arguments, nil)
	default:
		return llm.ContentPart{}, false
	}
}

type stream struct {
	core         *streamcore.Stream
	source       *ssestream.Stream[openaisdk.ChatCompletionChunk]
	thinking     *partState
	text         *partState
	toolCalls    map[int64]*partState
	nextIndex    int
	finishReason string
	started      bool
	sawRefusal   bool
	sawToolCall  bool
}

func (s *stream) Next() (llm.Event, error) {
	return s.core.Next(s)
}

func (s *stream) Advance() bool {
	return s.source.Next()
}

func (s *stream) Translate() ([]llm.Event, error) {
	return s.translate(s.source.Current())
}

func (s *stream) Finish() streamcore.Terminal {
	if err := s.source.Err(); err != nil {
		return streamcore.ReadFailure("openai completions", normalizeProviderError(err))
	}
	// A clean SDK EOF only means that SSE framing ended. Chat Completions
	// requires a non-empty finish_reason to prove the model response completed.
	if s.finishReason == "" {
		return streamcore.UnexpectedEOF("openai completions")
	}
	events, err := s.finish()
	if err != nil {
		return streamcore.Terminal{Err: err}
	}
	return streamcore.Terminal{Events: events}
}

func (s *stream) Close() error {
	if err := s.core.Close(s.source.Close); err != nil {
		return fmt.Errorf("openai completions: %w", err)
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
		s.core.Message.ResponseID = chunk.ID
	}
	if chunk.Model != "" && chunk.Model != s.core.Message.ModelID {
		s.core.Message.ResponseModelID = chunk.Model
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
		s.core.Parts.Partial(state.contentIndex, state)
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
		s.core.Parts.Partial(state.contentIndex, state)
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
		s.core.Parts.Partial(state.contentIndex, state)
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
	s.core.Usage = streamcore.UsageFromReport(
		s.core.Pricing,
		usage.PromptTokens,
		usage.CompletionTokens,
		usage.CompletionTokensDetails.ReasoningTokens,
		usage.PromptTokensDetails.CachedTokens,
		usage.PromptTokensDetails.CacheWriteTokens,
		usage.TotalTokens,
	)
}

// finish assembles the terminal events once the SSE stream ends. Tool calls
// must have valid JSON arguments; a truncated call is a protocol error and the
// agent loop will surface it instead of executing a partial call.
func (s *stream) finish() ([]llm.Event, error) {
	for _, state := range s.toolCalls {
		if _, err := streamcore.FinishToolCall(state.toolCall, state.arguments, nil); err != nil {
			return nil, fmt.Errorf("openai completions: %w", err)
		}
	}

	reason := stopReason(s.finishReason)
	if reason == llm.StopReasonStop {
		switch {
		case s.sawToolCall:
			reason = llm.StopReasonToolUse
		case s.sawRefusal:
			reason = llm.StopReasonRefusal
		}
	}

	snapshot := s.core.Parts.Snapshot()
	events := make([]llm.Event, 0, len(snapshot)+3)
	for index, part := range snapshot {
		content := part
		switch part.Type {
		case llm.ContentTypeText:
			events = append(events, llm.Event{
				Type:         llm.EventTypeTextEnd,
				ContentIndex: index,
				Content:      &content,
			})
		case llm.ContentTypeThinking:
			events = append(events, llm.Event{
				Type:         llm.EventTypeThinkingEnd,
				ContentIndex: index,
				Content:      &content,
			})
		case llm.ContentTypeToolCall:
			events = append(events, llm.Event{
				Type:         llm.EventTypeToolCallEnd,
				ContentIndex: index,
				Content:      &content,
				ToolCall:     content.ToolCall,
			})
		default:
			return nil, fmt.Errorf(
				"openai completions: content part has unknown type %q",
				part.Type,
			)
		}
	}
	return append(events, s.core.TerminalEvents(reason)...), nil
}

func stopReason(finishReason string) llm.StopReason {
	switch finishReason {
	case "stop":
		return llm.StopReasonStop
	case "length":
		return llm.StopReasonLength
	case "tool_calls", "function_call":
		return llm.StopReasonToolUse
	case "content_filter":
		return llm.StopReasonRefusal
	default:
		return llm.StopReasonUnknown
	}
}

func normalizeProviderError(err error) error {
	var apiErr *openaisdk.Error
	if !errors.As(err, &apiErr) {
		return streamcore.NormalizeError(err, nil)
	}
	var header http.Header
	if apiErr.Response != nil {
		header = apiErr.Response.Header
	}
	code := apiErr.Code
	if code == "" {
		code = apiErr.Type
	}
	return streamcore.NormalizeError(err, &streamcore.ErrorInfo{
		StatusCode: apiErr.StatusCode,
		Code:       code,
		Header:     header,
	})
}
