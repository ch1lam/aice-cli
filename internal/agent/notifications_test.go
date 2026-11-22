package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func noticeInput(t *testing.T, id string, delegations ...llm.ToolDelegation) agent.InputMessage {
	t.Helper()
	message := mustPrompt(t, "notice "+id)
	message.Notice = &llm.Notice{Delegations: delegations}
	return agent.InputMessage{ID: id, Message: message}
}

// sourceOf returns each input once, then nothing.
func sourceOf(inputs ...agent.InputMessage) agent.InputSource {
	return func() (agent.InputMessage, bool, error) {
		if len(inputs) == 0 {
			return agent.InputMessage{}, false, nil
		}
		next := inputs[0]
		inputs = inputs[1:]
		return next, true, nil
	}
}

// requestTexts renders the user-role texts of one model request in order.
func requestTexts(request llm.Request) string {
	var texts []string
	for _, message := range request.Messages {
		if user, ok := message.(llm.UserMessage); ok {
			texts = append(texts, user.Content[0].Text)
		}
	}
	return strings.Join(texts, " | ")
}

// A notification waiting when the run starts follows the prompt; one waiting
// after a tool round joins steering in the next turn; one waiting at a
// natural stop continues the interaction instead of ending it.
func TestLoopTakesNotificationsAtEachBoundary(t *testing.T) {
	t.Parallel()
	info := testModel()
	text := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	toolUse := assistantMessage(info, llm.StopReasonToolUse, toolCallPart("call-1", "echo", `{}`))
	for _, test := range []struct {
		name    string
		scripts []*streamScript
		// waiting reports, per poll, whether the notification is ready.
		waiting  func(polls int) bool
		steering bool
		want     []string
	}{
		{
			name:    "run start",
			scripts: []*streamScript{{events: terminalEvents(text)}},
			waiting: func(int) bool { return true },
			want:    []string{"hello | notice n1"},
		},
		{
			name:     "after a tool round with steering",
			scripts:  []*streamScript{{events: terminalEvents(toolUse)}, {events: terminalEvents(text)}},
			waiting:  func(polls int) bool { return polls > 1 },
			steering: true,
			want:     []string{"hello", "hello | steer | notice n1"},
		},
		{
			name:    "natural stop",
			scripts: []*streamScript{{events: terminalEvents(text)}, {events: terminalEvents(text)}},
			waiting: func(polls int) bool { return polls > 1 },
			want:    []string{"hello", "hello | notice n1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model := &scriptedModel{scripts: test.scripts}
			loop, err := agent.NewLoop(model, []agent.Tool{newFakeTool("echo", nil)}, agent.WithGuard(allowAllGuard{}))
			if err != nil {
				t.Fatal(err)
			}
			input := testInput(info, mustPrompt(t, "hello"))
			polls, notice := 0, sourceOf(noticeInput(t, "n1"))
			input.Notifications = func() (agent.InputMessage, bool, error) {
				polls++
				if !test.waiting(polls) {
					return agent.InputMessage{}, false, nil
				}
				return notice()
			}
			if test.steering {
				steer := mustPrompt(t, "steer")
				input.Steering = sourceOf(agent.InputMessage{ID: "s1", Message: steer})
			}
			var events []agent.AgentEvent
			result, err := loop.Run(t.Context(), input, collectEvents(&events))
			if err != nil {
				t.Fatal(err)
			}
			if len(model.requests) != len(test.want) {
				t.Fatalf("requests = %d, want %d", len(model.requests), len(test.want))
			}
			for index, want := range test.want {
				if got := requestTexts(model.requests[index]); got != want {
					t.Errorf("request %d users = %q, want %q", index, got, want)
				}
			}
			var announced, kept bool
			for _, event := range events {
				announced = announced || event.Type == agent.EventTypeMessageEnd && event.InputID == "n1" && event.InputKind == agent.InputKindNotification
			}
			for _, message := range result.Messages() {
				if user, ok := message.(llm.UserMessage); ok && user.IsNotice() {
					kept = true
				}
			}
			if !announced || !kept {
				t.Fatalf("notification announced = %v, kept in the result = %v", announced, kept)
			}
		})
	}
}

// A notification's delegations count toward the run that receives it like a
// subagent tool result's, also when the notification is the prompt.
func TestLoopCountsNotificationDelegations(t *testing.T) {
	t.Parallel()
	info := testModel()
	delegation := llm.ToolDelegation{SessionID: "child", Usage: llm.Usage{TotalTokens: 30}}
	for _, asPrompt := range []bool{false, true} {
		model := &scriptedModel{scripts: []*streamScript{
			{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
		}}
		loop, err := agent.NewLoop(model, nil, agent.WithRunLimits(agent.RunLimits{Tokens: 30}))
		if err != nil {
			t.Fatal(err)
		}
		input := testInput(info, mustPrompt(t, "hello"))
		if asPrompt {
			input.Prompt = noticeInput(t, "n1", delegation).Message
		} else {
			input.Notifications = sourceOf(noticeInput(t, "n1", delegation))
		}
		result, err := loop.Run(t.Context(), input, nil)
		if !errors.Is(err, agent.ErrTokenBudget) || len(model.requests) != 0 || result.Usage.TotalTokens != 30 {
			t.Fatalf("prompt %v: err = %v after %d requests, usage %d; want the delegated usage to exhaust the budget",
				asPrompt, err, len(model.requests), result.Usage.TotalTokens)
		}
	}
}

// AwaitInput keeps a run going at a natural stop: each awaited follow-up or
// notification starts another interaction, of the kind its message shows,
// and the run ends once it reports none.
func TestLoopAwaitsInput(t *testing.T) {
	t.Parallel()
	info := testModel()
	text := assistantMessage(info, llm.StopReasonStop, textPart("done"))
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(text)}, {events: terminalEvents(text)}, {events: terminalEvents(text)},
	}}
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := testInput(info, mustPrompt(t, "hello"))
	await := sourceOf(noticeInput(t, "n1"), agent.InputMessage{ID: "f1", Message: mustPrompt(t, "more")})
	input.AwaitInput = func(context.Context) (agent.InputMessage, bool, error) { return await() }
	var events []agent.AgentEvent
	if _, err := loop.Run(t.Context(), input, collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if got := requestTexts(model.requests[2]); len(model.requests) != 3 || got != "hello | notice n1 | more" {
		t.Fatalf("requests = %d, last users = %q", len(model.requests), got)
	}
	want := "agent_start turn_start#1 message_start#1:user message_end#1:user message_start#1:assistant message_end#1:assistant turn_end#1 interaction_end " +
		"turn_start#2 message_start#2:user message_end#2:user message_start#2:assistant message_end#2:assistant turn_end#2 interaction_end " +
		"turn_start#3 message_start#3:user message_end#3:user message_start#3:assistant message_end#3:assistant turn_end#3 interaction_end agent_end"
	if got := lifecycleTrace(events); got != want {
		t.Errorf("events:\n got %s\nwant %s", got, want)
	}
	kinds := map[string]agent.InputKind{}
	for _, event := range events {
		if event.Type == agent.EventTypeMessageStart && event.InputID != "" {
			kinds[event.InputID] = event.InputKind
		}
	}
	if kinds["n1"] != agent.InputKindNotification || kinds["f1"] != agent.InputKindFollowUp {
		t.Errorf("input kinds = %v, want n1 a notification and f1 a follow-up", kinds)
	}
}

// Cancelling a run while it awaits a notification ends it with the
// cancellation.
func TestLoopAwaitStopsOnCancel(t *testing.T) {
	t.Parallel()
	info := testModel()
	model := &scriptedModel{scripts: []*streamScript{
		{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
	}}
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	input := testInput(info, mustPrompt(t, "hello"))
	input.AwaitInput = func(ctx context.Context) (agent.InputMessage, bool, error) {
		cancel()
		<-ctx.Done()
		return agent.InputMessage{}, false, ctx.Err()
	}
	if _, err := loop.Run(ctx, input, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want cancellation", err)
	}
}

// Only a notification may carry llm.Notice, and a notification must: a
// person's input is never mistaken for AICE's, or the reverse.
func TestLoopRejectsInputOfTheWrongOrigin(t *testing.T) {
	t.Parallel()
	info := testModel()
	for _, test := range []struct {
		name  string
		input func(*testing.T, *agent.RunInput)
	}{
		{name: "steering notice", input: func(t *testing.T, input *agent.RunInput) {
			input.Steering = sourceOf(noticeInput(t, "s1"))
		}},
		{name: "plain notification", input: func(t *testing.T, input *agent.RunInput) {
			input.Notifications = sourceOf(agent.InputMessage{ID: "n1", Message: mustPrompt(t, "plain")})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model := &scriptedModel{scripts: []*streamScript{
				{events: terminalEvents(assistantMessage(info, llm.StopReasonStop, textPart("done")))},
			}}
			loop, err := agent.NewLoop(model, nil)
			if err != nil {
				t.Fatal(err)
			}
			input := testInput(info, mustPrompt(t, "hello"))
			test.input(t, &input)
			if _, err := loop.Run(t.Context(), input, nil); err == nil || !strings.Contains(err.Error(), "wrong origin") {
				t.Fatalf("err = %v, want a wrong-origin error", err)
			}
		})
	}
}
