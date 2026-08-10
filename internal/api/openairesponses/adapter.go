// Package openairesponses translates between AICE's provider-neutral LLM
// contracts and the OpenAI Responses wire protocol.
package openairesponses

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
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/ch1lam/aice-cli/internal/api/streamcore"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// API identifies the OpenAI Responses wire protocol.
const API llm.API = "openai-responses"

// Config contains transport settings resolved by a provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Adapter implements AICE's model stream contract with the official OpenAI Go
// SDK. It constructs the Responses service directly so unrelated OPENAI_*
// environment variables cannot override another provider's explicit settings.
type Adapter struct {
	client responses.ResponseService
}

// New constructs an OpenAI Responses adapter.
func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("openai responses: API key is required")
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

	return &Adapter{client: responses.NewResponseService(opts...)}, nil
}

func validateBaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("openai responses: parse base URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf(
			"openai responses: base URL scheme %q is not HTTP(S)",
			parsed.Scheme,
		)
	}
	if parsed.Host == "" {
		return errors.New("openai responses: base URL must include a host")
	}
	return nil
}

// Stream starts one model request. The returned stream owns the SDK response
// body and must be closed by the caller.
func (a *Adapter) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	if ctx == nil {
		return nil, errors.New("openai responses: nil context")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("openai responses: validate request: %w", err)
	}

	params, err := requestParams(request)
	if err != nil {
		return nil, err
	}

	source := a.client.NewStreaming(ctx, params)
	if err := source.Err(); err != nil {
		return nil, fmt.Errorf(
			"openai responses: start response stream: %w",
			normalizeProviderError(err),
		)
	}

	return &stream{
		core:   streamcore.NewStream(request.Model),
		source: source,
		blocks: make(map[int64]*blockState),
	}, nil
}

func requestParams(request llm.Request) (responses.ResponseNewParams, error) {
	if request.Model.API != API {
		return responses.ResponseNewParams{}, fmt.Errorf(
			"openai responses: model API %q does not match %q",
			request.Model.API,
			API,
		)
	}

	if err := streamcore.ValidateTemperature(request.Options.Temperature); err != nil {
		return responses.ResponseNewParams{}, fmt.Errorf("openai responses: %w", err)
	}
	maxTokens, err := streamcore.ResolveMaxTokens(request)
	if err != nil {
		return responses.ResponseNewParams{}, fmt.Errorf("openai responses: %w", err)
	}

	input, err := inputParams(request.Messages, request.Model)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	tools, err := toolParams(request.Tools)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	reasoning, err := reasoningParam(request.Options.Thinking)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
		MaxOutputTokens: param.NewOpt(maxTokens),
		Model:           shared.ResponsesModel(request.Model.ID),
		Reasoning:       reasoning,
		Store:           param.NewOpt(false),
		Tools:           tools,
	}
	if request.SystemPrompt != "" {
		params.Instructions = param.NewOpt(request.SystemPrompt)
	}
	if request.Options.Temperature != nil {
		params.Temperature = param.NewOpt(*request.Options.Temperature)
	}
	return params, nil
}

func reasoningParam(level llm.ThinkingLevel) (shared.ReasoningParam, error) {
	if level == llm.ThinkingLevelOff {
		return shared.ReasoningParam{Effort: shared.ReasoningEffortNone}, nil
	}
	effort, err := streamcore.ThinkingEffort(level)
	if err != nil {
		return shared.ReasoningParam{}, fmt.Errorf("openai responses: %w", err)
	}
	if effort == "" {
		return shared.ReasoningParam{}, nil
	}
	return shared.ReasoningParam{Effort: shared.ReasoningEffort(effort)}, nil
}

func inputParams(
	messages []llm.Message,
	target llm.Model,
) (responses.ResponseInputParam, error) {
	result := make(responses.ResponseInputParam, 0, len(messages))
	for messageIndex, message := range messages {
		switch value := message.(type) {
		case llm.UserMessage:
			items, err := userInputParams(value.Content)
			if err != nil {
				return nil, fmt.Errorf(
					"openai responses: message %d: %w",
					messageIndex,
					err,
				)
			}
			result = append(result, items...)
		case llm.AssistantMessage:
			items, err := assistantInputParams(value, target)
			if err != nil {
				return nil, fmt.Errorf(
					"openai responses: message %d: %w",
					messageIndex,
					err,
				)
			}
			result = append(result, items...)
		case llm.ToolResultMessage:
			item, err := toolResultInputParam(value.ToolCallID, value.Content)
			if err != nil {
				return nil, fmt.Errorf(
					"openai responses: message %d: %w",
					messageIndex,
					err,
				)
			}
			result = append(result, item)
		case nil:
			return nil, fmt.Errorf("openai responses: message %d is nil", messageIndex)
		default:
			return nil, fmt.Errorf(
				"openai responses: message %d has unsupported type %T",
				messageIndex,
				message,
			)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("openai responses: at least one input item is required")
	}
	return result, nil
}

func userInputParams(content []llm.ContentPart) ([]responses.ResponseInputItemUnionParam, error) {
	parts := make(responses.ResponseInputMessageContentListParam, 0, len(content))
	for index, part := range content {
		switch part.Type {
		case llm.ContentTypeText:
			parts = append(parts, responses.ResponseInputContentParamOfInputText(part.Text))
		case llm.ContentTypeImage:
			if part.Image == nil {
				return nil, fmt.Errorf("content %d: image payload is required", index)
			}
			image := responses.ResponseInputContentParamOfInputImage(
				responses.ResponseInputImageDetailAuto,
			)
			image.OfInputImage.ImageURL = param.NewOpt(streamcore.ImageDataURL(*part.Image))
			parts = append(parts, image)
		default:
			return nil, fmt.Errorf(
				"content %d: unsupported user content type %q",
				index,
				part.Type,
			)
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("user message content is required")
	}
	return []responses.ResponseInputItemUnionParam{
		responses.ResponseInputItemParamOfMessage(
			parts,
			responses.EasyInputMessageRoleUser,
		),
	}, nil
}

func assistantInputParams(
	message llm.AssistantMessage,
	target llm.Model,
) ([]responses.ResponseInputItemUnionParam, error) {
	sameModel := message.Provider == target.Provider &&
		message.API == target.API &&
		message.ModelID == target.ID
	content := message.Content
	if !sameModel {
		content = streamcore.ProjectThinkingToText(content)
	}
	result := make([]responses.ResponseInputItemUnionParam, 0, len(content))
	for index, part := range content {
		switch part.Type {
		case llm.ContentTypeText:
			if !sameModel {
				part.Signature = ""
			}
			result = append(result, assistantTextInputParam(part))
		case llm.ContentTypeThinking:
			item, err := reasoningInputParam(part)
			if err != nil {
				return nil, fmt.Errorf("content %d: %w", index, err)
			}
			result = append(result, item)
		case llm.ContentTypeToolCall:
			if part.ToolCall == nil {
				return nil, fmt.Errorf("content %d: tool call payload is required", index)
			}
			call := part.ToolCall
			if !json.Valid(call.Arguments) {
				return nil, fmt.Errorf(
					"content %d: tool call arguments are not valid JSON",
					index,
				)
			}
			item := responses.ResponseInputItemParamOfFunctionCall(
				string(call.Arguments),
				call.ID,
				call.Name,
			)
			if sameModel && call.Signature != "" {
				item.OfFunctionCall.ID = param.NewOpt(call.Signature)
			}
			result = append(result, item)
		default:
			return nil, fmt.Errorf(
				"content %d: unsupported assistant content type %q",
				index,
				part.Type,
			)
		}
	}
	return result, nil
}

func assistantTextInputParam(part llm.ContentPart) responses.ResponseInputItemUnionParam {
	if part.Signature == "" {
		return responses.ResponseInputItemParamOfMessage(
			part.Text,
			responses.EasyInputMessageRoleAssistant,
		)
	}
	return responses.ResponseInputItemParamOfOutputMessage(
		[]responses.ResponseOutputMessageContentUnionParam{{
			OfOutputText: &responses.ResponseOutputTextParam{
				Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
				Text:        part.Text,
			},
		}},
		part.Signature,
		responses.ResponseOutputMessageStatusCompleted,
	)
}

func reasoningInputParam(part llm.ContentPart) (responses.ResponseInputItemUnionParam, error) {
	if part.Redacted {
		return responses.ResponseInputItemUnionParam{}, errors.New(
			"redacted thinking cannot be represented as a Responses reasoning item",
		)
	}
	if part.Signature == "" {
		return responses.ResponseInputItemUnionParam{}, errors.New(
			"thinking signature is required for stateless Responses replay",
		)
	}

	var stored responses.ResponseReasoningItem
	if strings.HasPrefix(part.Signature, "{") &&
		json.Unmarshal([]byte(part.Signature), &stored) == nil &&
		stored.ID != "" {
		converted := stored.ToParam()
		return responses.ResponseInputItemUnionParam{OfReasoning: &converted}, nil
	}

	converted := responses.ResponseReasoningItemParam{
		ID:      part.Signature,
		Summary: []responses.ResponseReasoningItemSummaryParam{},
		Content: []responses.ResponseReasoningItemContentParam{{Text: part.Text}},
		Status:  responses.ResponseReasoningItemStatusCompleted,
	}
	return responses.ResponseInputItemUnionParam{OfReasoning: &converted}, nil
}

func toolResultInputParam(
	callID string,
	content []llm.ContentPart,
) (responses.ResponseInputItemUnionParam, error) {
	var (
		texts  []string
		output responses.ResponseFunctionCallOutputItemListParam
	)
	for index, part := range content {
		switch part.Type {
		case llm.ContentTypeText:
			texts = append(texts, part.Text)
			output = append(
				output,
				responses.ResponseFunctionCallOutputItemParamOfInputText(part.Text),
			)
		case llm.ContentTypeImage:
			if part.Image == nil {
				return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
					"tool result content %d: image payload is required",
					index,
				)
			}
			image := &responses.ResponseInputImageContentParam{
				Detail: responses.ResponseInputImageContentDetailAuto,
				ImageURL: param.NewOpt(
					streamcore.ImageDataURL(*part.Image),
				),
			}
			output = append(output, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputImage: image,
			})
		default:
			return responses.ResponseInputItemUnionParam{}, fmt.Errorf(
				"tool result content %d has unsupported type %q",
				index,
				part.Type,
			)
		}
	}

	if len(output) == len(texts) {
		return responses.ResponseInputItemParamOfFunctionCallOutput(
			callID,
			strings.Join(texts, "\n"),
		), nil
	}
	return responses.ResponseInputItemParamOfFunctionCallOutput(callID, output), nil
}

func toolParams(tools []llm.ToolDefinition) ([]responses.ToolUnionParam, error) {
	schemas, err := streamcore.DecodeToolSchemas(tools)
	if err != nil {
		return nil, fmt.Errorf("openai responses: %w", err)
	}
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for index, tool := range tools {
		converted := responses.ToolParamOfFunction(tool.Name, schemas[index], false)
		if tool.Description != "" {
			converted.OfFunction.Description = param.NewOpt(tool.Description)
		}
		result = append(result, converted)
	}
	return result, nil
}

type blockState struct {
	type_        llm.ContentType
	contentIndex int
	itemID       string
	text         strings.Builder
	toolCall     llm.ToolCall
	arguments    string
}

func (s *blockState) PartialContent() (llm.ContentPart, bool) {
	switch s.type_ {
	case llm.ContentTypeText:
		return streamcore.PartialText(s.text.String(), s.itemID), true
	case llm.ContentTypeThinking:
		return streamcore.PartialThinking(s.text.String(), s.itemID, false), true
	case llm.ContentTypeToolCall:
		return streamcore.PartialToolCall(s.toolCall, s.arguments, nil)
	default:
		return llm.ContentPart{}, false
	}
}

type stream struct {
	core        *streamcore.Stream
	source      *ssestream.Stream[responses.ResponseStreamEventUnion]
	blocks      map[int64]*blockState
	nextIndex   int
	sawRefusal  bool
	sawToolCall bool
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
		return streamcore.ReadFailure("openai responses", normalizeProviderError(err))
	}
	return streamcore.UnexpectedEOF("openai responses")
}

func (s *stream) Close() error {
	if err := s.core.Close(s.source.Close); err != nil {
		return fmt.Errorf("openai responses: %w", err)
	}
	return nil
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

func (s *stream) translate(event responses.ResponseStreamEventUnion) ([]llm.Event, error) {
	switch event.Type {
	case "response.created":
		s.mergeResponseMetadata(event.Response)
		return []llm.Event{{Type: llm.EventTypeStart}}, nil
	case "response.in_progress", "response.queued":
		return nil, nil
	case "response.output_item.added":
		return s.startItem(event.OutputIndex, event.Item)
	case "response.reasoning_text.delta",
		"response.reasoning_summary_text.delta":
		return s.textDelta(
			event.OutputIndex,
			llm.ContentTypeThinking,
			event.Delta,
		)
	case "response.output_text.delta":
		return s.textDelta(event.OutputIndex, llm.ContentTypeText, event.Delta)
	case "response.refusal.delta":
		s.sawRefusal = true
		return s.textDelta(event.OutputIndex, llm.ContentTypeText, event.Delta)
	case "response.function_call_arguments.delta":
		return s.toolCallDelta(event.OutputIndex, event.Delta)
	case "response.function_call_arguments.done":
		return s.toolCallArgumentsDone(event.OutputIndex, event.Arguments)
	case "response.output_item.done":
		return s.finishItem(event.OutputIndex, event.Item)
	case "response.content_part.added",
		"response.content_part.done",
		"response.reasoning_text.done",
		"response.reasoning_summary_part.added",
		"response.reasoning_summary_part.done",
		"response.reasoning_summary_text.done",
		"response.output_text.done",
		"response.refusal.done",
		"response.output_text.annotation.added":
		return nil, nil
	case "response.completed":
		return s.complete(event.Response, llm.StopReasonStop)
	case "response.incomplete":
		reason := llm.StopReasonLength
		if event.Response.IncompleteDetails.Reason == "content_filter" {
			reason = llm.StopReasonRefusal
		}
		return s.complete(event.Response, reason)
	case "response.failed":
		s.mergeResponseMetadata(event.Response)
		s.mergeUsage(event.Response.Usage)
		message := event.Response.Error.Message
		if message == "" {
			message = "response failed without error details"
		}
		if event.Response.Error.Code != "" {
			message = string(event.Response.Error.Code) + ": " + message
		}
		err := &llm.ProviderError{
			Code: string(event.Response.Error.Code),
			Err:  errors.New("openai responses: " + message),
		}
		return nil, err
	case "error":
		message := event.Message
		if event.Code != "" {
			message = event.Code + ": " + message
		}
		return nil, &llm.ProviderError{
			Code: event.Code,
			Err:  errors.New("openai responses: " + message),
		}
	default:
		return nil, fmt.Errorf(
			"openai responses: unsupported stream event %q",
			event.Type,
		)
	}
}

func (s *stream) startItem(
	outputIndex int64,
	item responses.ResponseOutputItemUnion,
) ([]llm.Event, error) {
	if _, exists := s.blocks[outputIndex]; exists {
		return nil, fmt.Errorf(
			"openai responses: output item %d started twice",
			outputIndex,
		)
	}

	state := &blockState{
		contentIndex: s.nextIndex,
		itemID:       item.ID,
	}
	s.nextIndex++
	s.core.Parts.Partial(state.contentIndex, state)

	switch value := item.AsAny().(type) {
	case responses.ResponseReasoningItem:
		state.type_ = llm.ContentTypeThinking
		s.blocks[outputIndex] = state
		return []llm.Event{{
			Type:         llm.EventTypeThinkingStart,
			ContentIndex: state.contentIndex,
		}}, nil
	case responses.ResponseOutputMessage:
		state.type_ = llm.ContentTypeText
		s.blocks[outputIndex] = state
		return []llm.Event{{
			Type:         llm.EventTypeTextStart,
			ContentIndex: state.contentIndex,
		}}, nil
	case responses.ResponseFunctionToolCall:
		state.type_ = llm.ContentTypeToolCall
		state.arguments = value.Arguments
		state.toolCall = llm.ToolCall{
			ID:        value.CallID,
			Name:      value.Name,
			Signature: value.ID,
		}
		s.blocks[outputIndex] = state
		s.sawToolCall = true
		return []llm.Event{{
			Type:         llm.EventTypeToolCallStart,
			ContentIndex: state.contentIndex,
			ToolCallDelta: &llm.ToolCallDelta{
				ID:   value.CallID,
				Name: value.Name,
			},
		}}, nil
	default:
		return nil, fmt.Errorf(
			"openai responses: unsupported output item %q",
			item.Type,
		)
	}
}

func (s *stream) textDelta(
	outputIndex int64,
	contentType llm.ContentType,
	delta string,
) ([]llm.Event, error) {
	state, exists := s.blocks[outputIndex]
	if !exists {
		return nil, fmt.Errorf(
			"openai responses: delta for unknown output item %d",
			outputIndex,
		)
	}
	if state.type_ != contentType {
		return nil, fmt.Errorf(
			"openai responses: %s delta for incompatible output item %d",
			contentType,
			outputIndex,
		)
	}
	state.text.WriteString(delta)

	eventType := llm.EventTypeTextDelta
	if contentType == llm.ContentTypeThinking {
		eventType = llm.EventTypeThinkingDelta
	}
	return []llm.Event{{
		Type:         eventType,
		ContentIndex: state.contentIndex,
		Delta:        delta,
	}}, nil
}

func (s *stream) toolCallDelta(
	outputIndex int64,
	delta string,
) ([]llm.Event, error) {
	state, exists := s.blocks[outputIndex]
	if !exists || state.type_ != llm.ContentTypeToolCall {
		return nil, fmt.Errorf(
			"openai responses: tool arguments delta for incompatible output item %d",
			outputIndex,
		)
	}
	state.arguments += delta
	return []llm.Event{{
		Type:         llm.EventTypeToolCallDelta,
		ContentIndex: state.contentIndex,
		ToolCallDelta: &llm.ToolCallDelta{
			ArgumentsDelta: delta,
		},
	}}, nil
}

func (s *stream) toolCallArgumentsDone(
	outputIndex int64,
	arguments string,
) ([]llm.Event, error) {
	state, exists := s.blocks[outputIndex]
	if !exists || state.type_ != llm.ContentTypeToolCall {
		return nil, fmt.Errorf(
			"openai responses: tool arguments done for incompatible output item %d",
			outputIndex,
		)
	}
	var events []llm.Event
	if strings.HasPrefix(arguments, state.arguments) {
		delta := strings.TrimPrefix(arguments, state.arguments)
		if delta != "" {
			events = append(events, llm.Event{
				Type:         llm.EventTypeToolCallDelta,
				ContentIndex: state.contentIndex,
				ToolCallDelta: &llm.ToolCallDelta{
					ArgumentsDelta: delta,
				},
			})
		}
	}
	state.arguments = arguments
	return events, nil
}

func (s *stream) finishItem(
	outputIndex int64,
	item responses.ResponseOutputItemUnion,
) ([]llm.Event, error) {
	state, exists := s.blocks[outputIndex]
	var events []llm.Event
	if !exists {
		started, err := s.startItem(outputIndex, item)
		if err != nil {
			return nil, err
		}
		events = append(events, started...)
		state = s.blocks[outputIndex]
	}
	delete(s.blocks, outputIndex)

	switch value := item.AsAny().(type) {
	case responses.ResponseReasoningItem:
		if state.type_ != llm.ContentTypeThinking {
			return nil, fmt.Errorf(
				"openai responses: reasoning completed incompatible output item %d",
				outputIndex,
			)
		}
		text := reasoningText(value)
		if text == "" {
			text = state.text.String()
		}
		signature := item.RawJSON()
		if signature == "" {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf(
					"openai responses: encode reasoning signature: %w",
					err,
				)
			}
			signature = string(encoded)
		}
		content := llm.NewThinkingContent(text, signature).Part()
		s.core.Parts.Complete(state.contentIndex, content)
		events = append(events, llm.Event{
			Type:         llm.EventTypeThinkingEnd,
			ContentIndex: state.contentIndex,
			Content:      &content,
		})
	case responses.ResponseOutputMessage:
		if state.type_ != llm.ContentTypeText {
			return nil, fmt.Errorf(
				"openai responses: message completed incompatible output item %d",
				outputIndex,
			)
		}
		text, refusal := outputMessageText(value)
		if text == "" {
			text = state.text.String()
		}
		s.sawRefusal = s.sawRefusal || refusal
		content := llm.NewTextContent(text).Part()
		content.Signature = value.ID
		s.core.Parts.Complete(state.contentIndex, content)
		events = append(events, llm.Event{
			Type:         llm.EventTypeTextEnd,
			ContentIndex: state.contentIndex,
			Content:      &content,
		})
	case responses.ResponseFunctionToolCall:
		if state.type_ != llm.ContentTypeToolCall {
			return nil, fmt.Errorf(
				"openai responses: function call completed incompatible output item %d",
				outputIndex,
			)
		}
		arguments := value.Arguments
		if arguments == "" {
			arguments = state.arguments
		}
		state.toolCall.ID = value.CallID
		state.toolCall.Name = value.Name
		state.toolCall.Signature = value.ID
		call, err := streamcore.FinishToolCall(state.toolCall, arguments, nil)
		if err != nil {
			return nil, fmt.Errorf("openai responses: %w", err)
		}
		content := llm.ContentPart{
			Type:     llm.ContentTypeToolCall,
			ToolCall: &call,
		}
		s.core.Parts.Complete(state.contentIndex, content)
		events = append(events, llm.Event{
			Type:         llm.EventTypeToolCallEnd,
			ContentIndex: state.contentIndex,
			Content:      &content,
			ToolCall:     &call,
		})
	default:
		return nil, fmt.Errorf(
			"openai responses: unsupported completed output item %q",
			item.Type,
		)
	}
	return events, nil
}

func reasoningText(item responses.ResponseReasoningItem) string {
	if len(item.Content) > 0 {
		parts := make([]string, 0, len(item.Content))
		for _, content := range item.Content {
			parts = append(parts, content.Text)
		}
		return strings.Join(parts, "\n\n")
	}
	parts := make([]string, 0, len(item.Summary))
	for _, summary := range item.Summary {
		parts = append(parts, summary.Text)
	}
	return strings.Join(parts, "\n\n")
}

func outputMessageText(item responses.ResponseOutputMessage) (string, bool) {
	var (
		text    strings.Builder
		refusal bool
	)
	for _, content := range item.Content {
		switch content.Type {
		case "output_text":
			text.WriteString(content.Text)
		case "refusal":
			text.WriteString(content.Refusal)
			refusal = true
		}
	}
	return text.String(), refusal
}

func (s *stream) complete(
	response responses.Response,
	reason llm.StopReason,
) ([]llm.Event, error) {
	if len(s.blocks) != 0 {
		return nil, errors.New(
			"openai responses: terminal response has incomplete output items",
		)
	}
	s.mergeResponseMetadata(response)
	s.mergeUsage(response.Usage)
	if reason == llm.StopReasonStop {
		switch {
		case s.sawToolCall:
			reason = llm.StopReasonToolUse
		case s.sawRefusal:
			reason = llm.StopReasonRefusal
		}
	}
	return s.core.TerminalEvents(reason), nil
}

func (s *stream) mergeResponseMetadata(response responses.Response) {
	if response.ID != "" {
		s.core.Message.ResponseID = response.ID
	}
	if model := string(response.Model); model != "" && model != s.core.Message.ModelID {
		s.core.Message.ResponseModelID = model
	}
}

func (s *stream) mergeUsage(usage responses.ResponseUsage) {
	s.core.Usage = streamcore.UsageFromReport(
		s.core.Pricing,
		usage.InputTokens,
		usage.OutputTokens,
		usage.OutputTokensDetails.ReasoningTokens,
		usage.InputTokensDetails.CachedTokens,
		usage.InputTokensDetails.CacheWriteTokens,
		usage.TotalTokens,
	)
}
