package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type printSink interface {
	Accept(ctx context.Context, event agent.AgentEvent) error
	Finish() error
}

func newPrintSink(
	format string,
	output io.Writer,
	diagnostics io.Writer,
) (printSink, error) {
	switch format {
	case "", "text":
		return newStreamPrinter(output, diagnostics), nil
	case "json":
		return newJSONPrinter(output), nil
	default:
		return nil, fmt.Errorf("app: unsupported print output format %q", format)
	}
}

type streamPrinter struct {
	output          io.Writer
	diagnostics     io.Writer
	now             func() time.Time
	toolStarts      map[string]time.Time
	totalUsage      llm.Usage
	pendingText     bool
	lastWrittenByte byte
	isFinished      bool
}

func newStreamPrinter(output, diagnostics io.Writer) *streamPrinter {
	return &streamPrinter{
		output:      output,
		diagnostics: diagnostics,
		now:         time.Now,
		toolStarts:  make(map[string]time.Time),
	}
}

func (p *streamPrinter) Accept(ctx context.Context, event agent.AgentEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	switch event.Type {
	case agent.EventTypeMessageUpdate:
		return p.writeTextDelta(event)
	case agent.EventTypeMessageEnd:
		return p.finishAssistant(event)
	case agent.EventTypeToolExecutionStart:
		return p.startTool(event)
	case agent.EventTypeToolExecutionEnd:
		return p.finishTool(event)
	case agent.EventTypeRetryStart:
		return p.startRetry(event)
	case agent.EventTypeRetryEnd:
		return p.finishRetry(event)
	default:
		return nil
	}
}

func (p *streamPrinter) writeTextDelta(event agent.AgentEvent) error {
	if event.AssistantMessageEvent == nil ||
		event.AssistantMessageEvent.Type != llm.EventTypeTextDelta ||
		event.AssistantMessageEvent.Delta == "" {
		return nil
	}

	delta := event.AssistantMessageEvent.Delta
	if _, err := io.WriteString(p.output, delta); err != nil {
		return fmt.Errorf("app: write streamed response: %w", err)
	}
	p.pendingText = true
	p.lastWrittenByte = delta[len(delta)-1]
	return nil
}

func (p *streamPrinter) Finish() error {
	if p.isFinished {
		return nil
	}
	p.isFinished = true
	if err := p.finishLine(); err != nil {
		return err
	}
	return p.writeProgress("aice: total %s", formatUsage(p.totalUsage))
}

func (p *streamPrinter) finishAssistant(event agent.AgentEvent) error {
	message, ok := event.Message.(llm.AssistantMessage)
	if !ok {
		return nil
	}
	if err := p.finishLine(); err != nil {
		return err
	}
	p.totalUsage = llm.AddUsage(p.totalUsage, message.Usage)
	return p.writeProgress(
		"aice: assistant stop_reason=%s %s",
		message.StopReason,
		formatUsage(message.Usage),
	)
}

func (p *streamPrinter) startTool(event agent.AgentEvent) error {
	if event.ToolCall == nil {
		return nil
	}
	p.toolStarts[event.ToolCall.ID] = p.now()
	detail := summarizeToolDetail(toolCallDetail(*event.ToolCall))
	if detail == "" {
		return p.writeProgress(
			"aice: tool name=%s status=started",
			event.ToolCall.Name,
		)
	}
	return p.writeProgress(
		"aice: tool name=%s status=started detail=%q",
		event.ToolCall.Name,
		detail,
	)
}

func (p *streamPrinter) finishTool(event agent.AgentEvent) error {
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
	if event.ToolResult != nil {
		isError = event.ToolResult.IsError
	}
	status := "succeeded"
	if isError {
		status = "failed"
	}
	return p.writeProgress(
		"aice: tool name=%s status=%s duration_ms=%d",
		event.ToolCall.Name,
		status,
		duration.Milliseconds(),
	)
}

func (p *streamPrinter) startRetry(event agent.AgentEvent) error {
	if event.Retry == nil {
		return nil
	}
	return p.writeProgress(
		"aice: retry attempt=%d max_retries=%d status=waiting delay_ms=%d",
		event.Retry.Attempt,
		event.Retry.MaxRetries,
		event.Retry.Delay.Milliseconds(),
	)
}

func (p *streamPrinter) finishRetry(event agent.AgentEvent) error {
	if event.Retry == nil {
		return nil
	}
	status := "failed"
	if event.Retry.Success {
		status = "succeeded"
	}
	return p.writeProgress(
		"aice: retry attempt=%d max_retries=%d status=%s",
		event.Retry.Attempt,
		event.Retry.MaxRetries,
		status,
	)
}

func (p *streamPrinter) writeProgress(format string, arguments ...any) error {
	if _, err := fmt.Fprintf(p.diagnostics, format+"\n", arguments...); err != nil {
		return fmt.Errorf("app: write print progress: %w", err)
	}
	return nil
}

func (p *streamPrinter) finishLine() error {
	if !p.pendingText {
		return nil
	}
	p.pendingText = false
	if p.lastWrittenByte == '\n' {
		return nil
	}
	if _, err := io.WriteString(p.output, "\n"); err != nil {
		return fmt.Errorf("app: finish streamed response: %w", err)
	}
	p.lastWrittenByte = '\n'
	return nil
}

func formatUsage(usage llm.Usage) string {
	formatted := fmt.Sprintf(
		"input_tokens=%d output_tokens=%d reasoning_tokens=%d cache_read_tokens=%d "+
			"cache_write_tokens=%d total_tokens=%d",
		usage.InputTokens,
		usage.OutputTokens,
		usage.ReasoningTokens,
		usage.CacheReadTokens,
		usage.CacheWriteTokens,
		usage.TotalTokens,
	)
	if usage.Cost != nil {
		formatted += fmt.Sprintf(" cost_usd=%.6f", usage.Cost.Total)
	}
	return formatted
}

func summarizeToolDetail(detail string) string {
	const maxRunes = 240

	detail = strings.Join(strings.Fields(detail), " ")
	runes := []rune(detail)
	if len(runes) <= maxRunes {
		return detail
	}
	return string(runes[:maxRunes-1]) + "…"
}
