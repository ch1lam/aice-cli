package tui

import (
	"reflect"
	"testing"
)

func TestCollectRunUpdatesPreservesOrderAndBoundaries(t *testing.T) {
	delta := runUpdate{event: DisplayEvent{Kind: DisplayEventAssistantDelta,
		Delta: DisplayDelta{Kind: DisplayDeltaThinking, Delta: "part"}}}
	end := runUpdate{event: DisplayEvent{Kind: DisplayEventAssistantEnd}}
	updates := make(chan runUpdate, maximumEventBatch+4)
	want := []runUpdate{{event: DisplayEvent{Kind: DisplayEventAssistantStart}}}
	for range maximumEventBatch + 1 {
		want = append(want, delta)
	}
	want = append(want, end, runUpdate{done: true})
	for _, update := range want {
		updates <- update
	}
	close(updates)
	var got []runUpdate
	for {
		batch := collectRunUpdates(updates)
		if len(batch.updates) > maximumEventBatch {
			t.Fatal("batch exceeded its event bound")
		}
		for i, update := range batch.updates {
			if update.event.Kind != DisplayEventAssistantDelta && i != len(batch.updates)-1 {
				t.Fatal("lifecycle update did not flush the batch")
			}
		}
		got = append(got, batch.updates...)
		if batch.closed {
			break
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("batching lost or reordered updates")
	}
}

func TestCollectRunUpdatesFlushesQuietStream(t *testing.T) {
	updates := make(chan runUpdate, 1)
	updates <- runUpdate{event: DisplayEvent{Kind: DisplayEventAssistantDelta}}
	batch := collectRunUpdates(updates)
	if len(batch.updates) != 1 || batch.closed {
		t.Fatal("quiet stream did not flush its pending delta")
	}
	close(updates)
}
