// Package interaction defines UI-neutral interactive lifecycle contracts and
// owns active-run input coordination.
package interaction

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ch1lam/aice-cli/internal/llm"
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
	ID     string
	Text   string
	Kind   DeliveryKind
	Images []llm.ImageContent
	Files  []string
	Skills []string
}

// Mailbox is the bounded synchronization point between an interactive UI and
// one active Agent run.
type Mailbox struct {
	mu       sync.Mutex
	entries  []Delivery
	isSealed bool
	// changed closes when a delivery arrives, for a run awaiting input.
	changed chan struct{}
}

// NewMailbox constructs an empty input mailbox for one Agent run.
func NewMailbox() *Mailbox {
	return &Mailbox{
		entries: make([]Delivery, 0, maximumPendingDeliveries),
		changed: make(chan struct{}),
	}
}

// Changed returns a channel that closes when the next delivery arrives. A
// waiter takes it before checking the mailbox, so no delivery slips between.
func (m *Mailbox) Changed() <-chan struct{} {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changed
}

// Deliver appends one input without blocking the active Agent run.
func (m *Mailbox) Deliver(delivery Delivery) error {
	if m == nil {
		return ErrClosed
	}
	if strings.TrimSpace(delivery.ID) == "" {
		return fmt.Errorf("interaction: delivery id is required")
	}
	if strings.TrimSpace(delivery.Text) == "" && len(delivery.Images) == 0 {
		return fmt.Errorf("interaction: delivery text is required")
	}
	switch delivery.Kind {
	case DeliveryKindSteer, DeliveryKindFollowUp:
	default:
		return fmt.Errorf("interaction: delivery kind %d is invalid", delivery.Kind)
	}
	if len(delivery.Files) != 0 || len(delivery.Skills) != 0 {
		return fmt.Errorf("interaction: file and skill references must be resolved before enqueueing")
	}
	if err := ValidateImages(delivery.Images); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isSealed {
		return ErrClosed
	}
	if len(m.entries) >= maximumPendingDeliveries {
		return ErrFull
	}
	delivery.Images = CloneImages(delivery.Images)
	m.entries = append(m.entries, delivery)
	close(m.changed)
	m.changed = make(chan struct{})
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
	next, ok := m.takeQueued()
	if !ok {
		m.isSealed = true
	}
	return next, ok
}

// TakeQueued is TakeFollowUp for a run that may still wait for other work:
// it never seals, so the person can keep sending while the run waits.
// SealIfEmpty then closes the boundary once the run decides to end.
func (m *Mailbox) TakeQueued() (Delivery, bool) {
	if m == nil {
		return Delivery{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.takeQueued()
}

// SealIfEmpty seals the mailbox unless a delivery waits, which the caller
// must take instead of ending.
func (m *Mailbox) SealIfEmpty() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) > 0 {
		return false
	}
	m.isSealed = true
	return true
}

func (m *Mailbox) takeQueued() (Delivery, bool) {
	for index := range m.entries {
		m.entries[index].Kind = DeliveryKindFollowUp
	}
	if len(m.entries) == 0 {
		return Delivery{}, false
	}
	next := m.entries[0]
	m.entries = m.entries[1:]
	return next, true
}

// Seal rejects future deliveries. Existing entries are never consumed by a
// completed run.
func (m *Mailbox) Seal() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.isSealed = true
	m.mu.Unlock()
}
