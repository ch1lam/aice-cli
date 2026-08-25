package app

import (
	"context"
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

type streamPrinter struct {
	output          io.Writer
	pendingText     bool
	lastWrittenByte byte
}

func (p *streamPrinter) Accept(ctx context.Context, event agent.AgentEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Type == agent.EventTypeMessageEnd {
		if _, ok := event.Message.(llm.AssistantMessage); ok {
			return p.finishLine()
		}
	}
	if event.Type != agent.EventTypeMessageUpdate || event.AssistantMessageEvent == nil {
		return nil
	}
	if event.AssistantMessageEvent.Type != llm.EventTypeTextDelta ||
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
	return p.finishLine()
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
