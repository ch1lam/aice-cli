package agent_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// lifecycleTrace renders the lifecycle events frontends depend on: turn,
// message and retry boundaries with their numbers and outcomes.
func lifecycleTrace(events []agent.AgentEvent) string {
	var parts []string
	for _, event := range events {
		failed := ""
		if event.Err != nil {
			failed = "!"
		}
		switch event.Type {
		case agent.EventTypeTurnStart, agent.EventTypeTurnEnd:
			parts = append(parts, fmt.Sprintf("%s#%d%s", event.Type, event.TurnNumber, failed))
		case agent.EventTypeMessageStart, agent.EventTypeMessageEnd:
			role := llm.RoleUnknown
			if event.Message != nil {
				role = event.Message.MessageRole()
			}
			parts = append(parts, fmt.Sprintf("%s#%d:%s%s", event.Type, event.TurnNumber, role, failed))
		case agent.EventTypeRetryStart:
			parts = append(parts, fmt.Sprintf("retry_start@%d", event.Retry.Attempt))
		case agent.EventTypeRetryEnd:
			parts = append(parts, fmt.Sprintf("retry_end@%d:%t", event.Retry.Attempt, event.Retry.Success))
		case agent.EventTypeAgentStart, agent.EventTypeAgentEnd, agent.EventTypeInteractionEnd:
			parts = append(parts, string(event.Type)+failed)
		}
	}
	return strings.Join(parts, " ")
}

// TestLoopEventLifecycleSequences pins the turn, message and retry boundaries
// of each Loop path; TUI, ACP and --print JSON all consume this order.
func TestLoopEventLifecycleSequences(t *testing.T) {
	t.Parallel()
	info := testModel()
	text := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	toolUse := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("call-1", "echo", `{}`))
	transient := &llm.ProviderError{StatusCode: http.StatusServiceUnavailable}
	failed := assistantMessage(info, llm.StopReasonError, textPart("partial"))
	failed.ErrorMessage = "unavailable"
	once := func(message string) agent.InputSource {
		sent := false
		return func() (agent.InputMessage, bool, error) {
			if sent {
				return agent.InputMessage{}, false, nil
			}
			sent = true
			prompt, err := llm.NewUserMessage(textPart(message))
			return agent.InputMessage{ID: message, Message: prompt}, true, err
		}
	}
	cut := &streamScript{events: []llm.Event{{Type: llm.EventTypeStart}}, nextErr: io.ErrUnexpectedEOF}

	tests := []struct {
		name    string
		model   agent.Model
		retries int
		input   func(*agent.RunInput)
		want    string
	}{
		{
			name:  "text",
			model: &scriptedModel{scripts: []*streamScript{{events: terminalEvents(text)}}},
			want:  "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant turn_end#1 interaction_end agent_end",
		},
		{
			name:  "tool round",
			model: &scriptedModel{scripts: []*streamScript{{events: terminalEvents(toolUse)}, {events: terminalEvents(text)}}},
			want:  "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant message_start#1:toolResult message_end#1:toolResult turn_end#1 turn_start#2 message_start#2:assistant message_end#2:assistant turn_end#2 interaction_end agent_end",
		},
		{
			name: "steering and follow-up",
			model: &scriptedModel{scripts: []*streamScript{
				{events: terminalEvents(text)}, {events: terminalEvents(text)}, {events: terminalEvents(text)},
			}},
			input: func(input *agent.RunInput) { input.Steering, input.FollowUp = once("steer"), once("follow") },
			want:  "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant turn_end#1 turn_start#2 message_start#2:user message_end#2:user message_start#2:assistant message_end#2:assistant turn_end#2 interaction_end turn_start#3 message_start#3:user message_end#3:user message_start#3:assistant message_end#3:assistant turn_end#3 interaction_end agent_end",
		},
		{
			name: "terminal error retried",
			model: &scriptedModel{scripts: []*streamScript{
				{events: []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeError, Message: &failed, Err: transient}}},
				{events: terminalEvents(text)},
			}},
			retries: 1,
			want:    "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant! turn_end#1! retry_start@1 turn_start#2 message_start#2:assistant message_end#2:assistant retry_end@1:true turn_end#2 interaction_end agent_end",
		},
		{
			name:    "start failure retried",
			model:   &startFailureModel{startErr: transient, script: &streamScript{events: terminalEvents(text)}},
			retries: 1,
			want:    "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant! turn_end#1! retry_start@1 turn_start#2 message_start#2:assistant message_end#2:assistant retry_end@1:true turn_end#2 interaction_end agent_end",
		},
		{
			name:    "cut stream retried",
			model:   &scriptedModel{scripts: []*streamScript{cut, {events: terminalEvents(text)}}},
			retries: 1,
			want:    "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant! turn_end#1! retry_start@1 turn_start#2 message_start#2:assistant message_end#2:assistant retry_end@1:true turn_end#2 interaction_end agent_end",
		},
		{
			name:    "retries exhausted",
			model:   &startFailureModel{startErr: transient, failAll: true},
			retries: 2,
			want:    "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant! turn_end#1! retry_start@1 turn_start#2 message_start#2:assistant message_end#2:assistant! turn_end#2! retry_start@2 turn_start#3 message_start#3:assistant message_end#3:assistant! turn_end#3! retry_end@2:false agent_end!",
		},
		{
			name:  "cut stream not retried",
			model: &scriptedModel{scripts: []*streamScript{{events: []llm.Event{{Type: llm.EventTypeStart}}, nextErr: io.ErrUnexpectedEOF}}},
			want:  "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant! turn_end#1! agent_end!",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tool := newFakeTool("echo", nil)
			loop, err := agent.NewLoop(tt.model, []agent.Tool{tool}, agent.WithGuard(allowAllGuard{}),
				agent.WithRetryPolicy(agent.RetryPolicy{MaxRetries: tt.retries}))
			if err != nil {
				t.Fatal(err)
			}
			input := testInput(info, mustPrompt(t, "hello"))
			if tt.input != nil {
				tt.input(&input)
			}
			var events []agent.AgentEvent
			_, _ = loop.Run(t.Context(), input, collectEvents(&events))
			if got := lifecycleTrace(events); got != tt.want {
				t.Errorf("events:\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}
