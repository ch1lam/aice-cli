package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type deliveryMode = interaction.DeliveryKind

const (
	deliverySteer = interaction.DeliveryKindSteer
	deliveryQueue = interaction.DeliveryKindFollowUp
)

type pendingDelivery struct {
	id   string
	text string
	mode deliveryMode
}

func (m model) submitDelivery(mode deliveryMode) (model, tea.Cmd, bool) {
	text := strings.TrimSpace(m.expandComposerText())
	if text == "" && len(m.composerImages()) == 0 {
		return m, nil, true
	}
	if m.activeRun == nil {
		m.status = "Current response is finishing; press Enter again when ready"
		return m, nil, true
	}

	m.nextDeliveryID++
	delivery := pendingDelivery{
		id:   deliveryID(m.nextDeliveryID),
		text: imageInputText(text, len(m.composerImages())),
		mode: mode,
	}
	input := interaction.Delivery{ID: delivery.id, Text: text, Kind: mode,
		Images: m.composerImages(), Files: interaction.FileReferences(m.input.Value())}
	draft := composerDraft{text: m.input.Value(), pastes: m.pastes}
	prepare := m.prepareDelivery
	if prepare == nil {
		prepare = deliveryCommand(context.Background())
	}
	command, cancel := prepare(m.activeRun, input, draft)
	m.cancelDelivery = cancel
	m.deliveryPending = true

	m.pendingDeliveries = append(m.pendingDeliveries, delivery)
	m.historyIndex = -1
	m.historyDraft = ""
	m.input.Reset()
	m.pastes = nil
	m.inputNotice = ""
	m.commandSelection = 0
	m.commandDismissed = false
	if mode == deliveryQueue {
		m.status = "Queued for the next response"
	} else {
		m.status = "Steer waiting for a safe point"
	}
	m.resizeLayout()
	m.refreshViewport(false)
	return m, command, true
}

func deliveryID(sequence uint64) string {
	return "delivery-" + strconv.FormatUint(sequence, 10)
}

func (m *model) removePendingDelivery(id string) (pendingDelivery, bool) {
	for index, delivery := range m.pendingDeliveries {
		if delivery.id != id {
			continue
		}
		m.pendingDeliveries = append(
			m.pendingDeliveries[:index],
			m.pendingDeliveries[index+1:]...,
		)
		return delivery, true
	}
	return pendingDelivery{}, false
}

func (m *model) promotePendingDeliveries() bool {
	changed := false
	for index := range m.pendingDeliveries {
		delivery := &m.pendingDeliveries[index]
		if delivery.mode == deliverySteer {
			delivery.mode = deliveryQueue
			changed = true
		}
	}
	return changed
}

func (m *model) startQueuedDelivery(delivery pendingDelivery) {
	m.promotePendingDeliveries()
	if pending, found := m.removePendingDelivery(delivery.id); found {
		delivery = pending
	}
	m.activeProcessID = 0
	m.entries = append(m.entries, transcriptEntry{
		kind: entryUser,
		text: delivery.text,
	})
	m.beginProcess()
	m.assistantEntry = -1
	m.status = "Starting follow-up response..."
	m.resizeLayout()
}

func (m *model) applySteer(steering InputDisplay) bool {
	delivery, found := m.removePendingDelivery(steering.ID)
	text := steering.Text
	if found {
		text = delivery.text
	}
	if strings.TrimSpace(text) == "" {
		return false
	}
	m.activeProcessID = 0
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: text})
	m.beginProcess()
	m.assistantEntry = -1
	m.status = "Steering response..."
	m.resizeLayout()
	return true
}

// deliveryResult closes preflight. Failed inputs return to the composer intact.
type deliveryResult struct {
	id    string
	draft composerDraft
	text  string
	err   error
}

func (m model) applyDeliveryResult(result deliveryResult) (tea.Model, tea.Cmd) {
	m.deliveryPending = false
	m.cancelDelivery = nil
	if result.err != nil {
		m.removePendingDelivery(result.id)
		m.input.SetValue(result.draft.text)
		m.pastes = result.draft.pastes
		m.inputNotice = result.err.Error()
		m.status = "Pending input was not accepted"
		if errors.Is(result.err, interaction.ErrFull) {
			m.status = "Pending input is full"
		}
		if errors.Is(result.err, interaction.ErrClosed) {
			m.status = "Current response just finished"
		}
	} else {
		m.promptHistory = appendPromptHistory(m.promptHistory, result.text)
	}
	m.resizeLayout()
	m.refreshViewport(false)
	if m.composerInputEnabled() {
		return m, m.input.Focus()
	}
	return m, nil
}

func deliveryCommand(ctx context.Context) func(ActiveRun, interaction.Delivery, composerDraft) (tea.Cmd, context.CancelFunc) {
	return func(active ActiveRun, input interaction.Delivery, draft composerDraft) (tea.Cmd, context.CancelFunc) {
		deliveryCtx, cancel := context.WithCancel(ctx)
		return func() tea.Msg {
			defer cancel()
			return deliveryResult{id: input.ID, draft: draft, text: input.Text, err: active.Deliver(deliveryCtx, input)}
		}, cancel
	}
}
