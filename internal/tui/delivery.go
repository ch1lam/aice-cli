package tui

import (
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
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil, true
	}
	if m.activeRun == nil {
		m.status = "Current response is finishing; press Enter again when ready"
		return m, nil, true
	}

	m.nextDeliveryID++
	delivery := pendingDelivery{
		id:   deliveryID(m.nextDeliveryID),
		text: text,
		mode: mode,
	}
	err := m.activeRun.Deliver(interaction.Delivery{
		ID:   delivery.id,
		Text: delivery.text,
		Kind: delivery.mode,
	})
	if err != nil {
		m.nextDeliveryID--
		m.status = "Pending input was not accepted"
		if errors.Is(err, interaction.ErrFull) {
			m.status = "Pending input is full"
		}
		if errors.Is(err, interaction.ErrClosed) {
			m.status = "Current response just finished"
		}
		return m, nil, true
	}

	m.pendingDeliveries = append(m.pendingDeliveries, delivery)
	m.promptHistory = appendPromptHistory(m.promptHistory, text)
	m.historyIndex = -1
	m.historyDraft = ""
	m.input.Reset()
	m.commandSelection = 0
	m.commandDismissed = false
	if mode == deliveryQueue {
		m.status = "Queued for the next response"
	} else {
		m.status = "Steer waiting for a safe point"
	}
	m.resizeLayout()
	m.refreshViewport(false)
	return m, nil, true
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
