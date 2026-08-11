// Package interaction defines UI-neutral interactive lifecycle contracts and
// owns active-run input coordination.
package interaction

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

const maximumPendingDeliveries = 8

var (
	// ErrClosed indicates that an Agent run already made its terminal input
	// decision and cannot accept another delivery.
	ErrClosed = errors.New("interaction: delivery mailbox is closed")
	// ErrFull indicates that the bounded pending-input capacity was reached.
	ErrFull = errors.New("interaction: delivery mailbox is full")
)

// DeliveryKind identifies when an accepted user input may enter the Agent
// Loop.
type DeliveryKind uint8

const (
	DeliveryKindUnknown DeliveryKind = iota
	DeliveryKindSteer
	DeliveryKindFollowUp
)

// Delivery is one caller-owned user input waiting for an active Agent run.
type Delivery struct {
	ID   string
	Text string
	Kind DeliveryKind
}

// Mailbox is the bounded synchronization point between an interactive UI and
// one active Agent run.
type Mailbox struct {
	mu       sync.Mutex
	entries  []Delivery
	isSealed bool
}

// NewMailbox constructs an empty input mailbox for one Agent run.
func NewMailbox() *Mailbox {
	return &Mailbox{
		entries: make([]Delivery, 0, maximumPendingDeliveries),
	}
}

// Deliver appends one input without blocking the active Agent run.
func (m *Mailbox) Deliver(delivery Delivery) error {
	if m == nil {
		return ErrClosed
	}
	if strings.TrimSpace(delivery.ID) == "" {
		return fmt.Errorf("interaction: delivery id is required")
	}
	if strings.TrimSpace(delivery.Text) == "" {
		return fmt.Errorf("interaction: delivery text is required")
	}
	switch delivery.Kind {
	case DeliveryKindSteer, DeliveryKindFollowUp:
	default:
		return fmt.Errorf("interaction: delivery kind %d is invalid", delivery.Kind)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isSealed {
		return ErrClosed
	}
	if len(m.entries) >= maximumPendingDeliveries {
		return ErrFull
	}
	m.entries = append(m.entries, delivery)
	return nil
}

// TakeSteering removes the oldest steering input, preserving queued follow-ups
// in submission order.
func (m *Mailbox) TakeSteering() (Delivery, bool) {
	if m == nil {
		return Delivery{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for index, delivery := range m.entries {
		if delivery.Kind != DeliveryKindSteer {
			continue
		}
		m.entries = append(m.entries[:index], m.entries[index+1:]...)
		return delivery, true
	}
	return Delivery{}, false
}

// TakeFollowUp atomically closes one natural Agent stop boundary. Remaining
// steers become follow-ups, the oldest input is returned, or the mailbox is
// sealed when no work remains. Sealing prevents a late accepted input from
// being stranded after the Agent Loop decides to end.
func (m *Mailbox) TakeFollowUp() (Delivery, bool) {
	if m == nil {
		return Delivery{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.entries {
		m.entries[index].Kind = DeliveryKindFollowUp
	}
	if len(m.entries) == 0 {
		m.isSealed = true
		return Delivery{}, false
	}

	next := m.entries[0]
	m.entries = m.entries[1:]
	return next, true
}

// Seal rejects future deliveries. Existing entries remain available to the
// owner for diagnostics but are never consumed by a completed run.
func (m *Mailbox) Seal() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.isSealed = true
	m.mu.Unlock()
}
