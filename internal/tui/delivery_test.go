package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDeliveryMailboxConvertsPromotesAndSeals(t *testing.T) {
	t.Parallel()

	mailbox := newDeliveryMailbox()
	if !mailbox.add(pendingDelivery{id: "first", text: "one", mode: deliverySteer}) ||
		!mailbox.add(pendingDelivery{id: "second", text: "two", mode: deliveryQueue}) ||
		!mailbox.add(pendingDelivery{id: "third", text: "three", mode: deliverySteer}) {
		t.Fatal("mailbox rejected valid pending deliveries")
	}
	if !mailbox.convertToQueue("first") {
		t.Fatal("mailbox did not convert waiting steer to queue")
	}
	steering, ok := mailbox.takeSteering()
	if !ok || steering != (SteeringInput{ID: "third", Text: "three"}) {
		t.Fatalf("takeSteering() = %#v, %v", steering, ok)
	}

	first, promoted, ok := mailbox.nextQueued()
	if !ok || first.id != "first" || len(promoted) != 0 {
		t.Fatalf("first nextQueued() = %#v, %v, promoted %v", first, ok, promoted)
	}
	second, promoted, ok := mailbox.nextQueued()
	if !ok || second.id != "second" || len(promoted) != 0 {
		t.Fatalf("second nextQueued() = %#v, %v, promoted %v", second, ok, promoted)
	}
	if _, _, ok := mailbox.nextQueued(); ok {
		t.Fatal("empty mailbox reported another queued delivery")
	}
	if mailbox.add(pendingDelivery{id: "late", text: "late", mode: deliverySteer}) {
		t.Fatal("sealed mailbox accepted a delivery after terminal decision")
	}
}

func TestDeliveryMailboxPromotesUnconsumedSteer(t *testing.T) {
	t.Parallel()

	mailbox := newDeliveryMailbox()
	mailbox.add(pendingDelivery{id: "steer", text: "follow up", mode: deliverySteer})
	next, promoted, ok := mailbox.nextQueued()
	if !ok || next.id != "steer" || next.mode != deliveryQueue {
		t.Fatalf("nextQueued() = %#v, %v", next, ok)
	}
	if want := []string{"steer"}; !reflect.DeepEqual(promoted, want) {
		t.Fatalf("promoted IDs = %v, want %v", promoted, want)
	}
}

func TestModelEnterSteersAndControlEnterQueues(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.running = true
	current.acceptsDelivery = true
	current.deliveries = newDeliveryMailbox()
	current.input.SetValue("first line\nsecond line")

	steered, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command != nil {
		t.Fatal("enter did not submit steer synchronously")
	}
	if len(steered.pendingDeliveries) != 1 ||
		steered.pendingDeliveries[0].mode != deliverySteer {
		t.Fatalf("pending steer = %#v", steered.pendingDeliveries)
	}
	if steered.input.Value() != "" || !steered.input.Focused() {
		t.Fatal("steer submission did not clear and retain the composer")
	}
	view := ansi.Strip(steered.composerView(80))
	for _, want := range []string{"first line...", "[queue]"} {
		if !strings.Contains(view, want) {
			t.Errorf("pending composer = %q, want %q", view, want)
		}
	}
	if strings.Contains(view, "second line") {
		t.Fatalf("pending composer exposed later multiline content: %q", view)
	}

	steered.input.SetValue("run after this")
	queued, command, handled := steered.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
		Mod:  tea.ModCtrl,
	})
	if !handled || command != nil {
		t.Fatal("ctrl+enter did not queue synchronously")
	}
	if len(queued.pendingDeliveries) != 2 ||
		queued.pendingDeliveries[1].mode != deliveryQueue {
		t.Fatalf("pending deliveries = %#v", queued.pendingDeliveries)
	}

	width := max(queued.width, minimumWidth)
	composerTop := lipglossHeightForPendingClick(queued, width)
	clicked := updateModel(t, queued, tea.MouseClickMsg(tea.Mouse{
		X:      width - 3,
		Y:      composerTop + 1,
		Button: tea.MouseLeft,
	}))
	if clicked.pendingDeliveries[0].mode != deliveryQueue {
		t.Fatalf("queue button did not convert steer: %#v", clicked.pendingDeliveries)
	}
	if !strings.Contains(ansi.Strip(clicked.composerView(80)), "[queued]") {
		t.Fatal("converted steer is not rendered as queued")
	}
}

func lipglossHeightForPendingClick(current model, width int) int {
	return lipgloss.Height(current.headerView(width)) +
		current.viewport.Height() +
		lipgloss.Height(current.commandMenuView(width))
}

func TestModelSteerEventMovesPendingInputIntoTranscript(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.running = true
	current.acceptsDelivery = true
	current.pendingDeliveries = []pendingDelivery{{
		id:   "steer-1",
		text: "focus on tests",
		mode: deliverySteer,
	}}

	changed, _ := current.applyAgentEvent(DisplayEvent{
		Kind: DisplayEventSteer,
		Steering: SteeringDisplay{
			ID:   "steer-1",
			Text: "focus on tests",
		},
	})
	if !changed || len(current.pendingDeliveries) != 0 {
		t.Fatalf("pending deliveries after steer = %#v", current.pendingDeliveries)
	}
	if len(current.entries) != 1 ||
		current.entries[0].kind != entryUser ||
		current.entries[0].text != "focus on tests" {
		t.Fatalf("steer transcript entries = %#v", current.entries)
	}
}

func TestRunControllerExecutesQueuedPromptAsNextRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		mode         deliveryMode
		wantPromoted []string
	}{
		{name: "direct queue", mode: deliveryQueue},
		{
			name:         "unconsumed steer",
			mode:         deliverySteer,
			wantPromoted: []string{"queued-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var prompts []string
			runner := runnerFunc(func(
				_ context.Context,
				input RunInput,
				_ DisplayEventSink,
			) error {
				prompts = append(prompts, input.Prompt)
				return nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			requests := make(chan runRequest)
			done := make(chan struct{})
			go serveRuns(ctx, runner, requests, done)

			mailbox := newDeliveryMailbox()
			mailbox.add(pendingDelivery{
				id:   "queued-1",
				text: "second prompt",
				mode: tt.mode,
			})
			updates := make(chan runUpdate, runUpdateBuffer)
			requests <- runRequest{
				prompt:     "first prompt",
				deliveries: mailbox,
				updates:    updates,
			}

			var started []string
			var promoted []string
			for update := range updates {
				promoted = append(promoted, update.promoted...)
				if update.started != nil {
					started = append(started, update.started.text)
				}
			}
			if want := []string{"first prompt", "second prompt"}; !reflect.DeepEqual(prompts, want) {
				t.Fatalf("runner prompts = %v, want %v", prompts, want)
			}
			if want := []string{"second prompt"}; !reflect.DeepEqual(started, want) {
				t.Fatalf("started queued prompts = %v, want %v", started, want)
			}
			if !reflect.DeepEqual(promoted, tt.wantPromoted) {
				t.Fatalf("promoted IDs = %v, want %v", promoted, tt.wantPromoted)
			}

			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("run controller did not stop")
			}
		})
	}
}
