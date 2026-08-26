package app

import (
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestTranslateAgentEventIgnoresUnrenderedEvents(t *testing.T) {
	t.Parallel()

	identity := llm.Model{ID: "test", API: "test", Provider: "test"}
	prompt := llm.UserMessage{Role: llm.RoleUser}
	toolResult := llm.ToolResultMessage{Role: llm.RoleToolResult}
	for _, event := range []agent.AgentEvent{
		{Type: agent.EventTypeUnknown},
		{Type: agent.EventTypeAgentStart},
		{Type: agent.EventTypeTurnStart, TurnNumber: 1},
		{Type: agent.EventTypeTurnEnd, TurnNumber: 1},
		{Type: agent.EventTypeMessageStart, Message: prompt},
		{Type: agent.EventTypeMessageEnd, Message: prompt},
		{Type: agent.EventTypeMessageStart, Message: toolResult},
		{Type: agent.EventTypeMessageEnd, Message: toolResult},
		{Type: agent.EventTypeMessageUpdate, AssistantMessageEvent: nil},
		{Type: agent.EventTypeMessageUpdate, AssistantMessageEvent: &llm.Event{
			Type: llm.EventTypeUsage,
		}},
		{Type: agent.EventTypeToolExecutionStart},
		{Type: agent.EventTypeToolExecutionEnd},
		{Type: agent.EventTypeRetryStart},
		{Type: agent.EventTypeRetryEnd},
	} {
		if display := translateAgentEvent(event); display != nil {
			t.Errorf(
				"translateAgentEvent(%s) = %#v, want nil",
				event.Type,
				display,
			)
		}
	}
	assistant := llm.NewAssistantMessage(identity)
	if display := translateAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageStart,
		Message: assistant,
	}); display == nil ||
		display.Kind != tui.DisplayEventAssistantStart {
		t.Errorf("message_start translation = %#v, want assistant start", display)
	}
}

func TestTranslateAgentEventStreamsAssistantDeltas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event *llm.Event
		want  tui.DisplayDelta
	}{
		{
			name:  "text delta",
			event: &llm.Event{Type: llm.EventTypeTextDelta, Delta: "answer"},
			want:  tui.DisplayDelta{Kind: tui.DisplayDeltaText, Delta: "answer"},
		},
		{
			name:  "thinking delta",
			event: &llm.Event{Type: llm.EventTypeThinkingDelta, Delta: "reasoning"},
			want:  tui.DisplayDelta{Kind: tui.DisplayDeltaThinking, Delta: "reasoning"},
		},
		{
			name:  "tool call start",
			event: &llm.Event{Type: llm.EventTypeToolCallStart},
			want:  tui.DisplayDelta{Kind: tui.DisplayDeltaToolCall},
		},
		{
			name:  "tool call delta",
			event: &llm.Event{Type: llm.EventTypeToolCallDelta},
			want:  tui.DisplayDelta{Kind: tui.DisplayDeltaToolCall},
		},
		{
			name:  "tool call end",
			event: &llm.Event{Type: llm.EventTypeToolCallEnd},
			want:  tui.DisplayDelta{Kind: tui.DisplayDeltaToolCall},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			display := translateAgentEvent(agent.AgentEvent{
				Type:                  agent.EventTypeMessageUpdate,
				AssistantMessageEvent: tt.event,
			})
			if display == nil ||
				display.Kind != tui.DisplayEventAssistantDelta ||
				display.Delta != tt.want {
				t.Fatalf("message_update translation = %#v, want %#v", display, tt.want)
			}
		})
	}
}

func TestTranslateAgentEventCompletesAssistant(t *testing.T) {
	t.Parallel()

	identity := llm.Model{ID: "test", API: "test", Provider: "test"}
	conclusion := llm.NewAssistantMessage(identity)
	conclusion.Content = []llm.ContentPart{
		llm.NewThinkingContent("reasoning", "").Part(),
		llm.NewTextContent("**Answer.**").Part(),
	}
	conclusion.StopReason = llm.StopReasonStop

	display := translateAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: conclusion,
	})
	want := &tui.DisplayEvent{
		Kind: tui.DisplayEventAssistantEnd,
		Assistant: tui.AssistantDisplay{
			Text:      "**Answer.**",
			Thinking:  "reasoning",
			Concludes: true,
		},
	}
	if display == nil || *display != *want {
		t.Fatalf("message_end translation = %#v, want %#v", display, want)
	}

	call := llm.ToolCall{ID: "call-1", Name: "read"}
	toolTurn := llm.NewAssistantMessage(identity)
	toolTurn.Content = []llm.ContentPart{
		llm.NewTextContent("checking").Part(),
		{Type: llm.ContentTypeToolCall, ToolCall: &call},
	}
	toolTurn.StopReason = llm.StopReasonToolUse
	display = translateAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: toolTurn,
	})
	if display == nil ||
		display.Kind != tui.DisplayEventAssistantEnd ||
		display.Assistant.Concludes {
		t.Fatalf("tool-turn translation = %#v, want non-concluding assistant", display)
	}

	errored := llm.NewAssistantMessage(identity)
	errored.StopReason = llm.StopReasonError
	display = translateAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeMessageEnd,
		Message: errored,
	})
	if display == nil ||
		display.Kind != tui.DisplayEventAssistantEnd ||
		display.Assistant.Concludes {
		t.Fatalf("error-turn translation = %#v, want non-concluding assistant", display)
	}
}

func TestTranslateAgentEventAcceptsSteer(t *testing.T) {
	t.Parallel()

	steering, err := llm.NewUserMessage(llm.NewTextContent("change direction").Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	display := translateAgentEvent(agent.AgentEvent{
		Type:      agent.EventTypeMessageEnd,
		InputID:   "steer-1",
		InputKind: agent.InputKindSteering,
		Message:   steering,
	})
	want := &tui.DisplayEvent{
		Kind: tui.DisplayEventSteer,
		Input: tui.InputDisplay{
			ID:   "steer-1",
			Text: "change direction",
		},
	}
	if display == nil || *display != *want {
		t.Fatalf("steer translation = %#v, want %#v", display, want)
	}
}

func TestTranslateAgentEventAcceptsFollowUp(t *testing.T) {
	t.Parallel()

	followUp, err := llm.NewUserMessage(llm.NewTextContent("continue").Part())
	if err != nil {
		t.Fatalf("NewUserMessage() error = %v", err)
	}
	display := translateAgentEvent(agent.AgentEvent{
		Type:      agent.EventTypeMessageEnd,
		InputID:   "follow-up-1",
		InputKind: agent.InputKindFollowUp,
		Message:   followUp,
	})
	want := &tui.DisplayEvent{
		Kind: tui.DisplayEventFollowUp,
		Input: tui.InputDisplay{
			ID:   "follow-up-1",
			Text: "continue",
		},
	}
	if display == nil || *display != *want {
		t.Fatalf("follow-up translation = %#v, want %#v", display, want)
	}
}

func TestTranslateAgentEventExtractsToolInput(t *testing.T) {
	t.Parallel()

	call := llm.ToolCall{
		ID:        "bash-call",
		Name:      "bash",
		Arguments: []byte(`{"command":"go test ./...\nprintf done"}`),
	}
	display := translateAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionStart,
		ToolCall: &call,
	})
	want := &tui.DisplayEvent{
		Kind: tui.DisplayEventToolStart,
		Tool: tui.ToolDisplay{
			ID:     "bash-call",
			Name:   "bash",
			Detail: "go test ./...\nprintf done",
		},
	}
	if display == nil || *display != *want {
		t.Fatalf("tool start translation = %#v, want %#v", display, want)
	}

	read := llm.ToolCall{
		ID:        "read-call",
		Name:      "read",
		Arguments: []byte(`{"path":"go.mod","offset":10}`),
	}
	display = translateAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionStart,
		ToolCall: &read,
	})
	if display == nil ||
		display.Kind != tui.DisplayEventToolStart ||
		display.Tool.Detail != "go.mod" {
		t.Fatalf("read translation = %#v, want path only", display)
	}

	skill := llm.ToolCall{
		ID:        "skill-call",
		Name:      "skill",
		Arguments: []byte(`{"name":"samber/cc-skills-golang@golang-how-to"}`),
	}
	display = translateAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionStart,
		ToolCall: &skill,
	})
	if display == nil ||
		display.Kind != tui.DisplayEventToolStart ||
		display.Tool.Detail != "samber/cc-skills-golang@golang-how-to" {
		t.Fatalf("skill translation = %#v, want skill name only", display)
	}

	failed := translateAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionEnd,
		ToolCall: &call,
		Err:      agent.ErrProtocol,
	})
	if failed == nil ||
		failed.Kind != tui.DisplayEventToolEnd ||
		!failed.Tool.Failed ||
		failed.Tool.ID != "bash-call" {
		t.Fatalf("tool end translation = %#v, want failed bash call", failed)
	}

	raw := llm.ToolCall{
		ID:        "raw-call",
		Name:      "read",
		Arguments: []byte(`not json`),
	}
	display = translateAgentEvent(agent.AgentEvent{
		Type:     agent.EventTypeToolExecutionStart,
		ToolCall: &raw,
	})
	if display == nil || display.Tool.Detail != "" {
		t.Fatalf("malformed arguments translation = %#v, want empty detail", display)
	}
}

func TestTranslateAgentEventRetriesAndAgentEnd(t *testing.T) {
	t.Parallel()

	display := translateAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeRetryStart,
		Retry: &agent.RetryEvent{
			Attempt:    2,
			MaxRetries: 3,
		},
	})
	want := &tui.DisplayEvent{
		Kind: tui.DisplayEventRetryStart,
		Retry: tui.RetryDisplay{
			Attempt:    2,
			MaxRetries: 3,
			Delay:      "0s",
		},
	}
	if display == nil || *display != *want {
		t.Fatalf("retry_start translation = %#v, want %#v", display, want)
	}

	display = translateAgentEvent(agent.AgentEvent{
		Type:  agent.EventTypeRetryEnd,
		Retry: &agent.RetryEvent{Success: true},
	})
	if display == nil ||
		display.Kind != tui.DisplayEventRetryEnd ||
		!display.Retry.Succeeded {
		t.Fatalf("retry_end translation = %#v, want succeeded", display)
	}

	display = translateAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentEnd})
	if display == nil ||
		display.Kind != tui.DisplayEventAgentEnd ||
		display.Err != nil {
		t.Fatalf("agent_end translation = %#v, want clean completion", display)
	}
}

func TestNewDisplayUsageFlattensCost(t *testing.T) {
	t.Parallel()

	usage := llm.Usage{
		InputTokens:      120,
		OutputTokens:     30,
		CacheReadTokens:  40,
		CacheWriteTokens: 5,
		TotalTokens:      195,
		Cost:             &llm.Cost{Input: 0.001, Output: 0.002, Total: 0.0031},
	}
	want := tui.DisplayUsage{
		InputTokens:      120,
		OutputTokens:     30,
		CacheReadTokens:  40,
		CacheWriteTokens: 5,
		TotalCost:        0.0031,
	}
	if got := newDisplayUsage(usage); got != want {
		t.Fatalf("newDisplayUsage() = %#v, want %#v", got, want)
	}

	noCost := llm.Usage{InputTokens: 1}
	if got := newDisplayUsage(noCost); got != (tui.DisplayUsage{InputTokens: 1}) {
		t.Fatalf("newDisplayUsage(no cost) = %#v, want zero cost", got)
	}
}

func TestDisplayThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		level   llm.ThinkingLevel
		want    tui.DisplayThinking
		wantErr bool
	}{
		{name: "unknown maps to default", level: llm.ThinkingLevelUnknown, want: tui.DisplayThinkingDefault},
		{name: "off", level: llm.ThinkingLevelOff, want: tui.DisplayThinkingOff},
		{name: "minimal", level: llm.ThinkingLevelMinimal, want: tui.DisplayThinkingMinimal},
		{name: "low", level: llm.ThinkingLevelLow, want: tui.DisplayThinkingLow},
		{name: "medium", level: llm.ThinkingLevelMedium, want: tui.DisplayThinkingMedium},
		{name: "high", level: llm.ThinkingLevelHigh, want: tui.DisplayThinkingHigh},
		{name: "xhigh", level: llm.ThinkingLevelXHigh, want: tui.DisplayThinkingXHigh},
		{name: "max", level: llm.ThinkingLevelMax, want: tui.DisplayThinkingMax},
		{name: "unsupported", level: llm.ThinkingLevel("nope"), want: tui.DisplayThinkingDefault, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := displayThinking(tt.level)
			if (err != nil) != tt.wantErr {
				t.Fatalf("displayThinking(%q) error = %v, wantErr %v", tt.level, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("displayThinking(%q) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}
