package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestModelEnterSteersAndControlEnterQueues(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.running = true
	current.acceptsDelivery = true
	var delivered []interaction.Delivery
	current.activeRun = &activeRunFunc{deliver: func(delivery interaction.Delivery) error {
		delivered = append(delivered, delivery)
		return nil
	}}
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
	if len(delivered) != 1 ||
		delivered[0].Kind != interaction.DeliveryKindSteer ||
		delivered[0].Text != "first line\nsecond line" {
		t.Fatalf("delivered steer = %#v", delivered)
	}
	if steered.input.Value() != "" || !steered.input.Focused() {
		t.Fatal("steer submission did not clear and retain the composer")
	}
	view := ansi.Strip(steered.composerView(80))
	if top, _, _ := strings.Cut(view, "\n"); lipgloss.Width(top) != 80 {
		t.Fatalf("composer width = %d, want 80", lipgloss.Width(top))
	}
	if strings.Contains(view, "first line") {
		t.Fatalf("pending steer remained in composer: %q", view)
	}
	transcript := ansi.Strip(steered.transcriptView())
	for _, want := range []string{"YOU", "first line", "second line"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("pending steer transcript = %q, want %q", transcript, want)
		}
	}
	if !strings.ContainsAny(transcript, "╎┊┆") {
		t.Fatalf("pending steer transcript has no dashed rail: %q", transcript)
	}
	updated, _ := steered.Update(spinner.TickMsg{Time: time.Now()})
	animated, ok := updated.(model)
	if !ok {
		t.Fatalf("spinner update model = %T, want tui.model", updated)
	}
	if next := ansi.Strip(animated.transcriptView()); next == transcript {
		t.Fatalf("pending steer rail did not animate: %q", next)
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
	if len(delivered) != 2 ||
		delivered[1].Kind != interaction.DeliveryKindFollowUp ||
		delivered[1].Text != "run after this" {
		t.Fatalf("delivered follow-up = %#v", delivered)
	}
	view = ansi.Strip(queued.composerView(80))
	for _, unwanted := range []string{"STEER", "QUEUE", "[steer]", "[queue]"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("queued composer = %q, unwanted mode text %q", view, unwanted)
		}
	}
	if !strings.Contains(view, "  ↳ run after this") {
		t.Fatalf("queued composer = %q, want indented return-arrow preview", view)
	}
	lines := strings.Split(view, "\n")
	queueLine := -1
	inputLine := -1
	for index, line := range lines {
		switch {
		case strings.Contains(line, "↳ run after this"):
			queueLine = index
		case strings.Contains(line, "┃ Ask about this workspace"):
			inputLine = index
		}
	}
	if queueLine < 0 || inputLine != queueLine+2 {
		t.Fatalf(
			"queued composer lines = %#v, want one blank line before input",
			lines,
		)
	}
	if strings.Contains(ansi.Strip(queued.transcriptView()), "run after this") {
		t.Fatal("queued input appeared in transcript before its run started")
	}
	if strings.Contains(view, "first line") || strings.Contains(view, "second line") {
		t.Fatalf("pending steer leaked back into composer: %q", view)
	}
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
		Input: InputDisplay{
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

func TestModelFollowUpEventStartsNextInteractionWithoutStartingAnotherRun(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.running = true
	current.activeRun = &activeRunFunc{}
	current.pendingDeliveries = []pendingDelivery{
		{id: "steer-1", text: "late steer", mode: deliverySteer},
		{id: "follow-up-1", text: "continue", mode: deliveryQueue},
	}

	changed, _ := current.applyAgentEvent(DisplayEvent{
		Kind: DisplayEventFollowUp,
		Input: InputDisplay{
			ID:   "follow-up-1",
			Text: "continue",
		},
	})
	if !changed {
		t.Fatal("follow-up event did not change presentation state")
	}
	if current.activeRun == nil {
		t.Fatal("follow-up event discarded the active run")
	}
	if len(current.pendingDeliveries) != 1 ||
		current.pendingDeliveries[0].mode != deliveryQueue {
		t.Fatalf("pending deliveries = %#v, want promoted late steer", current.pendingDeliveries)
	}
	if got, want := current.entries[len(current.entries)-1], (transcriptEntry{
		kind: entryUser,
		text: "continue",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("follow-up transcript entry = %#v, want %#v", got, want)
	}
}
