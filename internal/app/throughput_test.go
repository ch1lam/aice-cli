package app

import (
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestGenerationTimerMeasuresFirstContentToLastStreamEvent(t *testing.T) {
	t.Parallel()
	clock := time.Unix(0, 0)
	timer := generationTimer{now: func() time.Time { return clock }}
	update := func(at time.Duration, kind llm.EventType) {
		clock = time.Unix(0, 0).Add(at)
		if _, _, ok := timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageUpdate, AssistantMessageEvent: &llm.Event{Type: kind}}); ok {
			t.Fatalf("%s update reported a measurement", kind)
		}
	}
	timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageStart, Message: llm.AssistantMessage{}})
	update(time.Second, llm.EventTypeTextStart) // block markers carry no tokens
	update(3*time.Second, llm.EventTypeThinkingDelta)
	update(5*time.Second, llm.EventTypeTextDelta)
	update(7*time.Second, llm.EventTypeUsage)
	clock = clock.Add(time.Minute) // Session recording before message_end
	tokens, span, ok := timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageEnd, Message: llm.AssistantMessage{Usage: llm.Usage{OutputTokens: 200}}})
	if !ok || tokens != 200 || span != 4*time.Second {
		t.Fatalf("observe() = %d, %s, %v; want 200, 4s, true", tokens, span, ok)
	}
}

func TestGenerationTimerExcludesUnmeasurableCalls(t *testing.T) {
	t.Parallel()
	clock := time.Unix(0, 0)
	timer := generationTimer{now: func() time.Time { return clock }}
	call := func(span time.Duration, usage llm.Usage) bool {
		timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageStart, Message: llm.AssistantMessage{}})
		timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageUpdate, AssistantMessageEvent: &llm.Event{Type: llm.EventTypeTextDelta}})
		clock = clock.Add(span)
		timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageUpdate, AssistantMessageEvent: &llm.Event{Type: llm.EventTypeUsage}})
		_, _, ok := timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageEnd, Message: llm.AssistantMessage{Usage: usage}})
		return ok
	}
	if call(10*time.Millisecond, llm.Usage{OutputTokens: 500}) {
		t.Fatal("burst-delivered call was measured")
	}
	if call(time.Second, llm.Usage{}) {
		t.Fatal("call without reported output was measured")
	}
	if !call(time.Second, llm.Usage{OutputTokens: 50}) {
		t.Fatal("streamed call with output was not measured")
	}
	// A user message_end and an assistant without content are not calls.
	if _, _, ok := timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageEnd, Message: llm.UserMessage{}}); ok {
		t.Fatal("user message was measured")
	}
	timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageStart, Message: llm.AssistantMessage{}})
	if _, _, ok := timer.observe(agent.AgentEvent{Type: agent.EventTypeMessageEnd, Message: llm.AssistantMessage{Usage: llm.Usage{OutputTokens: 5}}}); ok {
		t.Fatal("call without streamed content was measured")
	}
}
