// Package anthropic translates between AICE's provider-neutral LLM contracts
// and the Anthropic Messages protocol.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/ch1lam/aice-cli/internal/api/streamcore"
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
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("anthropic: validate request: %w", err)
	}

	params, err := requestParams(request)
	if err != nil {
		return nil, err
	}

	source := a.client.Messages.NewStreaming(ctx, params)
	if err := source.Err(); err != nil {
		return nil, fmt.Errorf(
			"anthropic: start message stream: %w",
			normalizeProviderError(err),
		)
	}

	return &stream{
		core:   streamcore.NewStream(request.Model),
		source: source,
		blocks: make(map[int]*blockState),
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

	if err := streamcore.ValidateTemperature(request.Options.Temperature); err != nil {
		return anthropicsdk.MessageNewParams{}, fmt.Errorf("anthropic: %w", err)
	}
	maxTokens, err := streamcore.ResolveMaxTokens(request)
	if err != nil {
		return anthropicsdk.MessageNewParams{}, fmt.Errorf("anthropic: %w", err)
	}

	messages, err := messageParams(request.Messages, request.Model)
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
	effort, err := streamcore.ThinkingEffort(level)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	switch effort {
	case "":
		return nil
	case "off":
		disabled := anthropicsdk.NewThinkingConfigDisabledParam()
		params.Thinking.OfDisabled = &disabled
		return nil
	case "minimal":
		// The Anthropic protocol has no minimal effort level; the SDK collapses
		// it to low.
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortLow
	default:
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(1_024)
		params.OutputConfig.Effort = anthropicsdk.OutputConfigEffort(effort)
	}
	return nil
}

func messageParams(
	messages []llm.Message,
	target llm.Model,
) ([]anthropicsdk.MessageParam, error) {
	result := make([]anthropicsdk.MessageParam, 0, len(messages))
	for messageIndex, message := range messages {
		var (
			role    llm.Role
			content []llm.ContentPart
			blocks  []anthropicsdk.ContentBlockParamUnion
		)
		switch value := message.(type) {
		case llm.UserMessage:
			if err := value.Validate(); err != nil {
				return nil, fmt.Errorf("anthropic: message %d: %w", messageIndex, err)
			}
			role = value.Role
			content = value.Content
		case llm.AssistantMessage:
			if err := value.Validate(); err != nil {
				return nil, fmt.Errorf("anthropic: message %d: %w", messageIndex, err)
			}
			role = value.Role
			content = assistantContentForModel(value, target)
		case llm.ToolResultMessage:
			if err := value.Validate(); err != nil {
				return nil, fmt.Errorf("anthropic: message %d: %w", messageIndex, err)
			}
			role = value.Role
			block, err := toolResultBlockParam(&llm.ToolResult{
				CallID:  value.ToolCallID,
				Name:    value.ToolName,
				Content: value.Content,
				IsError: value.IsError,
			})
			if err != nil {
				return nil, fmt.Errorf("anthropic: message %d: %w", messageIndex, err)
			}
			blocks = []anthropicsdk.ContentBlockParamUnion{block}
		case nil:
			return nil, fmt.Errorf("anthropic: message %d is nil", messageIndex)
		default:
			return nil, fmt.Errorf(
				"anthropic: message %d has unsupported type %T",
				messageIndex,
				message,
			)
		}

		if blocks == nil {
			blocks = make([]anthropicsdk.ContentBlockParamUnion, 0, len(content))
		}
		for partIndex, part := range content {
			block, err := contentBlockParam(role, part)
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

		var converted anthropicsdk.MessageParam
		switch role {
		case llm.RoleUser, llm.RoleToolResult:
			converted = anthropicsdk.NewUserMessage(blocks...)
		case llm.RoleAssistant:
			converted = anthropicsdk.NewAssistantMessage(blocks...)
		default:
			return nil, fmt.Errorf("anthropic: message %d has unsupported role %q", messageIndex, role)
		}

		// Anthropic represents tool results as user content blocks and requires all
		// results for one assistant tool-use turn in the immediately following user
		// message. Provider-neutral history stores one ToolResultMessage per result,
		// so coalesce adjacent messages after role translation.
		if len(result) > 0 && result[len(result)-1].Role == converted.Role {
			result[len(result)-1].Content = append(result[len(result)-1].Content, blocks...)
			continue
		}
		result = append(result, converted)
	}
	return result, nil
}

func assistantContentForModel(
	message llm.AssistantMessage,
	target llm.Model,
) []llm.ContentPart {
	if message.Provider == target.Provider &&
		message.API == target.API &&
		message.ModelID == target.ID {
		return message.Content
	}
	return streamcore.ProjectThinkingToText(message.Content)
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
		if role != llm.RoleUser {
			return anthropicsdk.ContentBlockParamUnion{}, errors.New("tool result requires a user role")
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
		if err := streamcore.ValidateToolName(index, tool); err != nil {
			return nil, fmt.Errorf("anthropic: %w", err)
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

func (s *blockState) PartialContent() (llm.ContentPart, bool) {
	switch s.type_ {
	case llm.ContentTypeText:
		return streamcore.PartialText(s.text.String(), ""), true
	case llm.ContentTypeThinking:
		return streamcore.PartialThinking(s.text.String(), s.signature.String(), s.redacted), true
	case llm.ContentTypeToolCall:
		return streamcore.PartialToolCall(s.toolCall, s.text.String(), s.initialArguments)
	default:
		return llm.ContentPart{}, false
	}
}

type stream struct {
	core   *streamcore.Stream
	source *ssestream.Stream[anthropicsdk.MessageStreamEventUnion]
	blocks map[int]*blockState
	stop   llm.StopReason
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
		return streamcore.ReadFailure("anthropic", normalizeProviderError(err))
	}
	return streamcore.UnexpectedEOF("anthropic")
}

func (s *stream) Close() error {
	if err := s.core.Close(s.source.Close); err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	return nil
}

func normalizeProviderError(err error) error {
	var apiErr *anthropicsdk.Error
	if !errors.As(err, &apiErr) {
		return streamcore.NormalizeError(err, nil)
	}
	var header http.Header
	if apiErr.Response != nil {
		header = apiErr.Response.Header
	}
	return streamcore.NormalizeError(err, &streamcore.ErrorInfo{
		StatusCode: apiErr.StatusCode,
		Code:       string(apiErr.Type()),
		Header:     header,
	})
}

func (s *stream) translate(event anthropicsdk.MessageStreamEventUnion) ([]llm.Event, error) {
	switch value := event.AsAny().(type) {
	case anthropicsdk.MessageStartEvent:
		s.core.Message.ResponseID = value.Message.ID
		if responseModel := string(value.Message.Model); responseModel != "" &&
			responseModel != s.core.Message.ModelID {
			s.core.Message.ResponseModelID = responseModel
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
		return []llm.Event{s.core.UsageEvent()}, nil
	case anthropicsdk.MessageStopEvent:
		if len(s.blocks) != 0 {
			return nil, errors.New("anthropic: message stopped with incomplete content blocks")
		}
		return []llm.Event{s.core.Done(s.stop)}, nil
	default:
		return nil, fmt.Errorf("anthropic: unsupported stream event %q", event.Type)
	}
}

func (s *stream) startBlock(
	index int,
	block anthropicsdk.ContentBlockStartEventContentBlockUnion,
) ([]llm.Event, error) {
	if s.core.Parts.Has(index) {
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
	s.core.Parts.Partial(index, state)
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

	var (
		content llm.ContentPart
		event   llm.Event
	)
	switch state.type_ {
	case llm.ContentTypeText:
		content = llm.NewTextContent(state.text.String()).Part()
		event = llm.Event{Type: llm.EventTypeTextEnd, ContentIndex: index}
	case llm.ContentTypeThinking:
		thinking := llm.NewThinkingContent(state.text.String(), state.signature.String())
		thinking.Redacted = state.redacted
		content = thinking.Part()
		event = llm.Event{Type: llm.EventTypeThinkingEnd, ContentIndex: index}
	case llm.ContentTypeToolCall:
		call, err := streamcore.FinishToolCall(state.toolCall, state.text.String(), state.initialArguments)
		if err != nil {
			return nil, fmt.Errorf("anthropic: %w", err)
		}
		content = llm.ContentPart{Type: llm.ContentTypeToolCall, ToolCall: &call}
		event = llm.Event{
			Type:         llm.EventTypeToolCallEnd,
			ContentIndex: index,
			ToolCall:     &call,
		}
	default:
		return nil, fmt.Errorf("anthropic: content block %d has unknown type %q", index, state.type_)
	}
	s.core.Parts.Complete(index, content)
	event.Content = &content
	return []llm.Event{event}, nil
}

func (s *stream) mergeUsage(usage anthropicsdk.Usage) {
	s.core.Usage = streamcore.RecomputeTotal(s.core.Pricing, llm.Usage{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		ReasoningTokens:  usage.OutputTokensDetails.ThinkingTokens,
		CacheReadTokens:  usage.CacheReadInputTokens,
		CacheWriteTokens: usage.CacheCreationInputTokens,
	})
}

func (s *stream) mergeDeltaUsage(usage anthropicsdk.MessageDeltaUsage) {
	if usage.JSON.InputTokens.Valid() {
		s.core.Usage.InputTokens = usage.InputTokens
	}
	if usage.JSON.OutputTokens.Valid() {
		s.core.Usage.OutputTokens = usage.OutputTokens
	}
	if usage.JSON.OutputTokensDetails.Valid() {
		s.core.Usage.ReasoningTokens = usage.OutputTokensDetails.ThinkingTokens
	}
	if usage.JSON.CacheReadInputTokens.Valid() {
		s.core.Usage.CacheReadTokens = usage.CacheReadInputTokens
	}
	if usage.JSON.CacheCreationInputTokens.Valid() {
		s.core.Usage.CacheWriteTokens = usage.CacheCreationInputTokens
	}
	s.core.Usage = streamcore.RecomputeTotal(s.core.Pricing, s.core.Usage)
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
