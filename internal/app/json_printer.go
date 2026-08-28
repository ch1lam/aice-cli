package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	maxJSONToolResultBytes = 16 * 1024
	toolResultTruncation   = "\n...[truncated]"
)

type jsonPrinter struct {
	encoder    *json.Encoder
	now        func() time.Time
	toolStarts map[string]time.Time
	totalUsage llm.Usage
}

func newJSONPrinter(output io.Writer) *jsonPrinter {
	return &jsonPrinter{
		encoder:    json.NewEncoder(output),
		now:        time.Now,
		toolStarts: make(map[string]time.Time),
	}
}

func (p *jsonPrinter) Accept(ctx context.Context, event agent.AgentEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var payload any
	switch event.Type {
	case agent.EventTypeAgentStart:
		payload = jsonTypeEvent{Type: event.Type}
	case agent.EventTypeMessageEnd:
		payload = p.messageEnd(event)
	case agent.EventTypeToolExecutionStart:
		payload = p.toolStart(event)
	case agent.EventTypeToolExecutionEnd:
		payload = p.toolEnd(event)
	case agent.EventTypeRetryStart:
		payload = newRetryStartJSONEvent(event)
	case agent.EventTypeRetryEnd:
		payload = newRetryEndJSONEvent(event)
	case agent.EventTypeAgentEnd:
		payload = agentEndJSONEvent{
			Type:  event.Type,
			Usage: p.totalUsage,
			Error: errorText(event.Err),
		}
	default:
		return nil
	}
	if payload == nil {
		return nil
	}
	if err := p.encoder.Encode(payload); err != nil {
		return fmt.Errorf("app: encode print event %q: %w", event.Type, err)
	}
	return nil
}

func (*jsonPrinter) Finish() error {
	return nil
}

func (p *jsonPrinter) messageEnd(event agent.AgentEvent) any {
	message, ok := event.Message.(llm.AssistantMessage)
	if !ok {
		return nil
	}
	text, thinking := assistantContent(message)
	p.totalUsage = llm.AddUsage(p.totalUsage, message.Usage)
	model := message.ResponseModelID
	if model == "" {
		model = message.ModelID
	}
	return messageEndJSONEvent{
		Type:       event.Type,
		Role:       message.Role,
		Text:       text,
		Thinking:   thinking,
		Usage:      message.Usage,
		StopReason: message.StopReason,
		Model:      model,
	}
}

func (p *jsonPrinter) toolStart(event agent.AgentEvent) any {
	if event.ToolCall == nil {
		return nil
	}
	p.toolStarts[event.ToolCall.ID] = p.now()
	return toolStartJSONEvent{
		Type:       event.Type,
		ToolCallID: event.ToolCall.ID,
		Name:       event.ToolCall.Name,
		Arguments:  event.ToolCall.Arguments,
	}
}

func (p *jsonPrinter) toolEnd(event agent.AgentEvent) any {
	if event.ToolCall == nil {
		return nil
	}
	started, ok := p.toolStarts[event.ToolCall.ID]
	delete(p.toolStarts, event.ToolCall.ID)
	var duration time.Duration
	if ok {
		duration = p.now().Sub(started)
		if duration < 0 {
			duration = 0
		}
	}

	isError := event.Err != nil
	result := errorText(event.Err)
	if event.ToolResult != nil {
		isError = event.ToolResult.IsError
		result = printToolResultText(*event.ToolResult)
	}
	return toolEndJSONEvent{
		Type:       event.Type,
		ToolCallID: event.ToolCall.ID,
		Name:       event.ToolCall.Name,
		IsError:    isError,
		Result:     truncateToolResult(result),
		DurationMS: duration.Milliseconds(),
	}
}

type jsonTypeEvent struct {
	Type agent.EventType `json:"type"`
}

type messageEndJSONEvent struct {
	Type       agent.EventType `json:"type"`
	Role       llm.Role        `json:"role"`
	Text       string          `json:"text"`
	Thinking   string          `json:"thinking"`
	Usage      llm.Usage       `json:"usage"`
	StopReason llm.StopReason  `json:"stop_reason"`
	Model      string          `json:"model"`
}

type toolStartJSONEvent struct {
	Type       agent.EventType `json:"type"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
}

type toolEndJSONEvent struct {
	Type       agent.EventType `json:"type"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	IsError    bool            `json:"is_error"`
	Result     string          `json:"result"`
	DurationMS int64           `json:"duration_ms"`
}

type retryStartJSONEvent struct {
	Type       agent.EventType `json:"type"`
	Attempt    int             `json:"attempt"`
	MaxRetries int             `json:"max_retries"`
	DelayMS    int64           `json:"delay_ms"`
}

type retryEndJSONEvent struct {
	Type    agent.EventType `json:"type"`
	Attempt int             `json:"attempt"`
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
}

type agentEndJSONEvent struct {
	Type  agent.EventType `json:"type"`
	Usage llm.Usage       `json:"usage"`
	Error string          `json:"error,omitempty"`
}

func newRetryStartJSONEvent(event agent.AgentEvent) any {
	if event.Retry == nil {
		return nil
	}
	return retryStartJSONEvent{
		Type:       event.Type,
		Attempt:    event.Retry.Attempt,
		MaxRetries: event.Retry.MaxRetries,
		DelayMS:    event.Retry.Delay.Milliseconds(),
	}
}

func newRetryEndJSONEvent(event agent.AgentEvent) any {
	if event.Retry == nil {
		return nil
	}
	return retryEndJSONEvent{
		Type:    event.Type,
		Attempt: event.Retry.Attempt,
		Success: event.Retry.Success,
		Error:   errorText(event.Retry.Err),
	}
}

func printToolResultText(message llm.ToolResultMessage) string {
	var text strings.Builder
	for _, part := range message.Content {
		if part.Type == llm.ContentTypeText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func truncateToolResult(result string) string {
	result = strings.ToValidUTF8(result, "�")
	if len(result) <= maxJSONToolResultBytes {
		return result
	}
	limit := maxJSONToolResultBytes - len(toolResultTruncation)
	for limit > 0 && !utf8.RuneStart(result[limit]) {
		limit--
	}
	return result[:limit] + toolResultTruncation
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
