package app

import (
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// minimumGeneration excludes calls whose content arrived in one burst: their
// span measures delivery, not generation, and would dominate the average.
const minimumGeneration = 100 * time.Millisecond

// generationTimer measures one model call from its first streamed content to
// its last stream event, which is normally the provider's usage receipt. The
// event sink runs synchronously in the Agent Loop, so arrival time is stream
// time; Session recording after the stream is excluded.
type generationTimer struct {
	now         func() time.Time
	first, last time.Time
}

// observe returns the measured output tokens and span on an assistant
// message_end; ok is false for every other event and for unmeasurable calls.
func (t *generationTimer) observe(event agent.AgentEvent) (tokens int64, span time.Duration, ok bool) {
	switch event.Type {
	case agent.EventTypeMessageStart:
		if _, assistant := event.Message.(llm.AssistantMessage); assistant {
			t.first, t.last = time.Time{}, time.Time{}
		}
	case agent.EventTypeMessageUpdate:
		if event.AssistantMessageEvent == nil {
			return 0, 0, false
		}
		at := t.now()
		if t.first.IsZero() && streamedContent(event.AssistantMessageEvent.Type) {
			t.first = at
		}
		t.last = at
	case agent.EventTypeMessageEnd:
		message, assistant := event.Message.(llm.AssistantMessage)
		if !assistant || t.first.IsZero() {
			return 0, 0, false
		}
		span = t.last.Sub(t.first)
		t.first, t.last = time.Time{}, time.Time{}
		if message.Usage.OutputTokens <= 0 || span < minimumGeneration {
			return 0, 0, false
		}
		return message.Usage.OutputTokens, span, true
	}
	return 0, 0, false
}

func streamedContent(kind llm.EventType) bool {
	switch kind {
	case llm.EventTypeTextDelta, llm.EventTypeThinkingDelta,
		llm.EventTypeToolCallStart, llm.EventTypeToolCallDelta:
		return true
	}
	return false
}
