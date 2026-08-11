package interaction

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestMailboxOrdersPromotesAndSeals(t *testing.T) {
	t.Parallel()

	mailbox := NewMailbox()
	for _, delivery := range []Delivery{
		{ID: "first", Text: "one", Kind: DeliveryKindSteer},
		{ID: "second", Text: "two", Kind: DeliveryKindFollowUp},
		{ID: "third", Text: "three", Kind: DeliveryKindSteer},
	} {
		if err := mailbox.Deliver(delivery); err != nil {
			t.Fatalf("Deliver(%q) error = %v", delivery.ID, err)
		}
	}

	steering, ok := mailbox.TakeSteering()
	if !ok || steering.ID != "first" {
		t.Fatalf("TakeSteering() = %#v, %v", steering, ok)
	}

	first, ok := mailbox.TakeFollowUp()
	if !ok || first.ID != "second" || first.Kind != DeliveryKindFollowUp {
		t.Fatalf("first TakeFollowUp() = %#v, %v", first, ok)
	}
	second, ok := mailbox.TakeFollowUp()
	if !ok || second.ID != "third" || second.Kind != DeliveryKindFollowUp {
		t.Fatalf("second TakeFollowUp() = %#v, %v", second, ok)
	}
	if _, ok := mailbox.TakeFollowUp(); ok {
		t.Fatal("empty mailbox reported another follow-up")
	}
	if err := mailbox.Deliver(Delivery{
		ID:   "late",
		Text: "late",
		Kind: DeliveryKindSteer,
	}); !errors.Is(err, ErrClosed) {
		t.Fatalf("late Deliver() error = %v, want ErrClosed", err)
	}
}

func TestMailboxPreservesSubmissionOrderAcrossModes(t *testing.T) {
	t.Parallel()

	mailbox := NewMailbox()
	deliveries := []Delivery{
		{ID: "follow-up", Text: "later", Kind: DeliveryKindFollowUp},
		{ID: "steer", Text: "now", Kind: DeliveryKindSteer},
	}
	for _, delivery := range deliveries {
		if err := mailbox.Deliver(delivery); err != nil {
			t.Fatalf("Deliver(%q) error = %v", delivery.ID, err)
		}
	}

	steering, ok := mailbox.TakeSteering()
	if !ok || steering.ID != "steer" {
		t.Fatalf("TakeSteering() = %#v, %v", steering, ok)
	}
	followUp, ok := mailbox.TakeFollowUp()
	if !ok || followUp.ID != "follow-up" {
		t.Fatalf("TakeFollowUp() = %#v, %v", followUp, ok)
	}
}

func TestMailboxRejectsInvalidAndExcessDeliveries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		delivery Delivery
	}{
		{name: "missing id", delivery: Delivery{Text: "text", Kind: DeliveryKindSteer}},
		{name: "missing text", delivery: Delivery{ID: "id", Kind: DeliveryKindSteer}},
		{name: "unknown kind", delivery: Delivery{ID: "id", Text: "text"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := NewMailbox().Deliver(test.delivery); err == nil {
				t.Fatal("Deliver() error = nil")
			}
		})
	}

	mailbox := NewMailbox()
	accepted := make([]string, 0, maximumPendingDeliveries)
	for index := range maximumPendingDeliveries {
		id := string(rune('a' + index))
		if err := mailbox.Deliver(Delivery{
			ID:   id,
			Text: id,
			Kind: DeliveryKindFollowUp,
		}); err != nil {
			t.Fatalf("Deliver(%q) error = %v", id, err)
		}
		accepted = append(accepted, id)
	}
	if err := mailbox.Deliver(Delivery{
		ID:   "overflow",
		Text: "overflow",
		Kind: DeliveryKindFollowUp,
	}); !errors.Is(err, ErrFull) {
		t.Fatalf("overflow Deliver() error = %v, want ErrFull", err)
	}

	got := make([]string, 0, maximumPendingDeliveries)
	for {
		delivery, ok := mailbox.TakeFollowUp()
		if !ok {
			break
		}
		got = append(got, delivery.ID)
	}
	if !reflect.DeepEqual(got, accepted) {
		t.Fatalf("follow-up order = %v, want %v", got, accepted)
	}
}

func TestMailboxNeverStrandsAnAcceptedDeliveryAtTerminalBoundary(t *testing.T) {
	t.Parallel()

	for range 1_000 {
		mailbox := NewMailbox()
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var deliverErr error
		var followUp Delivery
		var followsUp bool
		go func() {
			defer wait.Done()
			<-start
			deliverErr = mailbox.Deliver(Delivery{
				ID:   "racing-input",
				Text: "continue",
				Kind: DeliveryKindFollowUp,
			})
		}()
		go func() {
			defer wait.Done()
			<-start
			followUp, followsUp = mailbox.TakeFollowUp()
		}()
		close(start)
		wait.Wait()

		switch {
		case deliverErr == nil:
			if !followsUp || followUp.ID != "racing-input" {
				t.Fatalf(
					"accepted delivery was stranded: follow-up %#v, %v",
					followUp,
					followsUp,
				)
			}
		case errors.Is(deliverErr, ErrClosed):
			if followsUp {
				t.Fatalf("closed delivery unexpectedly produced %#v", followUp)
			}
		default:
			t.Fatalf("Deliver() error = %v, want nil or ErrClosed", deliverErr)
		}
	}
}
