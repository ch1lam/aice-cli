package tui

import (
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

const maximumPendingDeliveries = 8

type deliveryMode uint8

const (
	deliverySteer deliveryMode = iota + 1
	deliveryQueue
)

type pendingDelivery struct {
	id   string
	text string
	mode deliveryMode
}

// deliveryMailbox is the single synchronization point shared by Bubble Tea,
// the run controller, and the active Agent Loop. Its bounded slice preserves
// submission order across steer polling and run-boundary queue promotion.
type deliveryMailbox struct {
	mu      sync.Mutex
	entries []pendingDelivery
	sealed  bool
}

func newDeliveryMailbox() *deliveryMailbox {
	return &deliveryMailbox{
		entries: make([]pendingDelivery, 0, maximumPendingDeliveries),
	}
}

func (m *deliveryMailbox) add(delivery pendingDelivery) bool {
	if m == nil || delivery.id == "" || strings.TrimSpace(delivery.text) == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed || len(m.entries) >= maximumPendingDeliveries {
		return false
	}
	m.entries = append(m.entries, delivery)
	return true
}

func (m *deliveryMailbox) takeSteering() (SteeringInput, bool) {
	if m == nil {
		return SteeringInput{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for index, entry := range m.entries {
		if entry.mode != deliverySteer {
			continue
		}
		m.entries = append(m.entries[:index], m.entries[index+1:]...)
		return SteeringInput{ID: entry.id, Text: entry.text}, true
	}
	return SteeringInput{}, false
}

// nextQueued atomically closes a completed run boundary: remaining steers are
// promoted to queue, the oldest queued input is removed for the next run, or
// the mailbox is sealed when no work remains. Sealing prevents the terminal
// race where the UI accepts an input after the controller has decided to stop.
func (m *deliveryMailbox) nextQueued() (pendingDelivery, []string, bool) {
	if m == nil {
		return pendingDelivery{}, nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	promoted := make([]string, 0)
	for index := range m.entries {
		entry := &m.entries[index]
		if entry.mode == deliverySteer {
			entry.mode = deliveryQueue
			promoted = append(promoted, entry.id)
		}
	}
	if len(m.entries) == 0 {
		m.sealed = true
		return pendingDelivery{}, promoted, false
	}

	next := m.entries[0]
	m.entries = m.entries[1:]
	return next, promoted, true
}

func (m model) submitDelivery(mode deliveryMode) (model, tea.Cmd, bool) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil, true
	}
	if m.deliveries == nil {
		m.status = "Current response is finishing; press Enter again when ready"
		return m, nil, true
	}

	m.nextDeliveryID++
	delivery := pendingDelivery{
		id:   deliveryID(m.nextDeliveryID),
		text: text,
		mode: mode,
	}
	if !m.deliveries.add(delivery) {
		m.nextDeliveryID--
		m.status = "Pending input is full or the current response just finished"
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

func (m *model) promotePendingDeliveries(ids []string) bool {
	changed := false
	for _, id := range ids {
		for index := range m.pendingDeliveries {
			delivery := &m.pendingDeliveries[index]
			if delivery.id == id && delivery.mode == deliverySteer {
				delivery.mode = deliveryQueue
				changed = true
				break
			}
		}
	}
	return changed
}

func (m *model) startQueuedDelivery(delivery pendingDelivery) {
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
	m.cancelRun = nil
	m.cancelRequested = false
	m.status = "Starting queued response..."
	m.resizeLayout()
}

func (m *model) applySteer(steering SteeringDisplay) bool {
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
