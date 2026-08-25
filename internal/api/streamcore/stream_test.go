package streamcore

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func testModel() llm.Model {
	return llm.Model{
		ID:       "test-model",
		API:      "test",
		Provider: "test",
	}
}

type scriptedItem struct {
	events []llm.Event
	err    error
}

type scriptedSource struct {
	items  []scriptedItem
	index  int
	finish func() Terminal
}

func (s *scriptedSource) Advance() bool {
	return s.index < len(s.items)
}

func (s *scriptedSource) Translate() ([]llm.Event, error) {
	item := s.items[s.index]
	s.index++
	return item.events, item.err
}

func (s *scriptedSource) Finish() Terminal {
	if s.finish != nil {
		return s.finish()
	}
	return Terminal{}
}

func drain(t *testing.T, stream *Stream, source Source) []llm.Event {
	t.Helper()
	var events []llm.Event
	for {
		event, err := stream.Next(source)
		if err == io.EOF {
			return events
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		events = append(events, event)
	}
}

func TestStreamNext(t *testing.T) {
	t.Parallel()

	start := llm.Event{Type: llm.EventTypeStart}
	delta := llm.Event{Type: llm.EventTypeTextDelta, Delta: "hi"}

	tests := []struct {
		name       string
		items      []scriptedItem
		finish     func(*Stream) Terminal
		wantTypes  []llm.EventType
		wantErr    bool
		wantReason llm.StopReason
	}{
		{
			name: "yields translate events then source exhaustion",
			items: []scriptedItem{
				{events: []llm.Event{start, delta}},
			},
			wantTypes: []llm.EventType{llm.EventTypeStart, llm.EventTypeTextDelta},
		},
		{
			name: "skips empty translate results",
			items: []scriptedItem{
				{},
				{events: []llm.Event{start}},
			},
			wantTypes: []llm.EventType{llm.EventTypeStart},
		},
		{
			name: "translate error becomes a terminal error event",
			items: []scriptedItem{
				{err: errors.New("decode failed")},
			},
			wantTypes:  []llm.EventType{llm.EventTypeError},
			wantErr:    true,
			wantReason: llm.StopReasonError,
		},
		{
			name: "canceled translate error uses aborted stop reason",
			items: []scriptedItem{
				{err: context.Canceled},
			},
			wantTypes:  []llm.EventType{llm.EventTypeError},
			wantErr:    true,
			wantReason: llm.StopReasonAborted,
		},
		{
			name:      "empty finish is eof",
			wantTypes: nil,
		},
		{
			name: "finish error becomes a terminal error event",
			finish: func(*Stream) Terminal {
				return Terminal{Err: errors.New("boom"), Message: "model stream failed"}
			},
			wantTypes:  []llm.EventType{llm.EventTypeError},
			wantErr:    true,
			wantReason: llm.StopReasonError,
		},
		{
			name: "finish events include usage and done",
			finish: func(stream *Stream) Terminal {
				return Terminal{Events: stream.TerminalEvents(llm.StopReasonStop)}
			},
			wantTypes:  []llm.EventType{llm.EventTypeUsage, llm.EventTypeDone},
			wantReason: llm.StopReasonStop,
		},
		{
			name: "read failure uses unexpected-eof helper",
			finish: func(*Stream) Terminal {
				return UnexpectedEOF("test")
			},
			wantTypes:  []llm.EventType{llm.EventTypeError},
			wantErr:    true,
			wantReason: llm.StopReasonError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stream := NewStream(testModel())
			source := &scriptedSource{items: test.items}
			if test.finish != nil {
				source.finish = func() Terminal { return test.finish(stream) }
			}

			events := drain(t, stream, source)
			if len(events) != len(test.wantTypes) {
				t.Fatalf("event count = %d, want %d (%v)", len(events), len(test.wantTypes), eventTypes(events))
			}
			for i, event := range events {
				if event.Type != test.wantTypes[i] {
					t.Fatalf("event[%d] type = %q, want %q", i, event.Type, test.wantTypes[i])
				}
			}
			if test.wantErr {
				last := events[len(events)-1]
				if last.Err == nil {
					t.Fatal("terminal error event Err is nil")
				}
			}
			if test.wantReason != "" {
				last := events[len(events)-1]
				if last.StopReason != test.wantReason {
					t.Fatalf("stop reason = %q, want %q", last.StopReason, test.wantReason)
				}
			}

			if _, err := stream.Next(source); err != io.EOF {
				t.Fatalf("Next() after drain error = %v, want EOF", err)
			}
		})
	}
}

func TestStreamNextQueuesPendingFromOneTranslate(t *testing.T) {
	t.Parallel()

	stream := NewStream(testModel())
	source := &scriptedSource{
		items: []scriptedItem{{
			events: []llm.Event{
				{Type: llm.EventTypeStart},
				{Type: llm.EventTypeTextDelta, Delta: "a"},
				{Type: llm.EventTypeTextDelta, Delta: "b"},
			},
		}},
	}

	first, err := stream.Next(source)
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if first.Type != llm.EventTypeStart {
		t.Fatalf("first event type = %q, want start", first.Type)
	}
	if source.index != 1 {
		t.Fatalf("source index = %d, want 1 after first Next", source.index)
	}

	second, err := stream.Next(source)
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if second.Type != llm.EventTypeTextDelta || second.Delta != "a" {
		t.Fatalf("second event = %#v, want text_delta a", second)
	}
	if source.index != 1 {
		t.Fatalf("pending drain advanced the source: index = %d", source.index)
	}
}

func TestStreamClose(t *testing.T) {
	t.Parallel()

	stream := NewStream(testModel())
	closes := 0
	if err := stream.Close(func() error {
		closes++
		return nil
	}); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(func() error {
		t.Fatal("close func called twice")
		return nil
	}); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if closes != 1 {
		t.Fatalf("close calls = %d, want 1", closes)
	}

	if _, err := stream.Next(&scriptedSource{
		items: []scriptedItem{{events: []llm.Event{{Type: llm.EventTypeStart}}}},
	}); err != io.EOF {
		t.Fatalf("Next() after Close error = %v, want EOF", err)
	}
}

func TestStreamCloseWrapsSourceError(t *testing.T) {
	t.Parallel()

	stream := NewStream(testModel())
	err := stream.Close(func() error { return errors.New("socket closed") })
	if err == nil || err.Error() != "close response stream: socket closed" {
		t.Fatalf("Close() error = %v, want wrapped socket closed", err)
	}
	if err := stream.Close(func() error {
		t.Fatal("close func called after a failed close")
		return nil
	}); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestReadFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "generic read error",
			err:         errors.New("eof"),
			wantMessage: "test: model stream failed",
		},
		{
			name:        "canceled",
			err:         context.Canceled,
			wantMessage: "test: request canceled",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			terminal := ReadFailure("test", test.err)
			if terminal.Err == nil {
				t.Fatal("ReadFailure() Err is nil")
			}
			if terminal.Message != test.wantMessage {
				t.Fatalf("ReadFailure() message = %q, want %q", terminal.Message, test.wantMessage)
			}
			if !errors.Is(terminal.Err, test.err) {
				t.Fatalf("ReadFailure() err = %v, want wrapping %v", terminal.Err, test.err)
			}
		})
	}
}

func eventTypes(events []llm.Event) []llm.EventType {
	types := make([]llm.EventType, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}
