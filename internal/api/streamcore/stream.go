// Package streamcore contains the provider-neutral streaming skeleton shared
// by AICE's protocol adapters. Adapters translate SDK events into llm events
// and delegate stream plumbing, terminal assembly, and shared request
// validation here.
package streamcore

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/llm"
)

// Source is the protocol-specific half of a normalized stream.
type Source interface {
	// Advance moves to the next raw protocol item.
	Advance() bool
	// Translate converts the current raw item into normalized events.
	Translate() ([]llm.Event, error)
	// Finish produces terminal events after the raw source is exhausted.
	Finish() Terminal
}

// Terminal carries the outcome of a source that stopped advancing.
type Terminal struct {
	Events  []llm.Event
	Err     error
	Message string
}

// Stream owns the normalized event queue and terminal state shared by every
// adapter stream. Adapters embed it and keep protocol-specific state next to it.
type Stream struct {
	Message llm.AssistantMessage
	Pricing llm.Pricing
	Usage   llm.Usage
	Parts   PartRegistry

	pending  []llm.Event
	finished bool
	closed   bool
}

// NewStream constructs the shared stream state for one model request.
func NewStream(model llm.Model) *Stream {
	return &Stream{
		Message: llm.NewAssistantMessage(model),
		Pricing: model.Pricing,
		Parts:   *NewPartRegistry(),
	}
}

// Next drains the event queue and then advances the source until an event, a
// terminal failure, or EOF.
func (s *Stream) Next(source Source) (llm.Event, error) {
	if len(s.pending) > 0 {
		return s.shift(), nil
	}
	if s.finished || s.closed {
		return llm.Event{}, io.EOF
	}

	for source.Advance() {
		events, err := source.Translate()
		if err != nil {
			return s.errorEvent(err, err.Error()), nil
		}
		if len(events) == 0 {
			continue
		}
		s.pending = events
		return s.shift(), nil
	}

	terminal := source.Finish()
	if terminal.Err != nil {
		message := terminal.Message
		if message == "" {
			message = terminal.Err.Error()
		}
		return s.errorEvent(terminal.Err, message), nil
	}
	if len(terminal.Events) == 0 {
		return llm.Event{}, io.EOF
	}
	s.pending = terminal.Events
	return s.shift(), nil
}

// Close marks the stream closed and closes the underlying source exactly once.
func (s *Stream) Close(close func() error) error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := close(); err != nil {
		return fmt.Errorf("close response stream: %w", err)
	}
	return nil
}

// Done marks the stream finished and builds the done event from the current
// snapshot.
func (s *Stream) Done(reason llm.StopReason) llm.Event {
	s.finished = true
	message := s.Snapshot(reason, "")
	return llm.Event{
		Type:       llm.EventTypeDone,
		StopReason: reason,
		Message:    &message,
	}
}

// Snapshot assembles the current assistant message state.
func (s *Stream) Snapshot(reason llm.StopReason, errorMessage string) llm.AssistantMessage {
	message := s.Message
	message.Content = s.Parts.Snapshot()
	message.Usage = s.Usage
	message.StopReason = reason
	message.ErrorMessage = errorMessage
	return message
}

// UsageEvent builds a usage event from the current usage.
func (s *Stream) UsageEvent() llm.Event {
	usage := s.Usage
	return llm.Event{Type: llm.EventTypeUsage, Usage: &usage}
}

// TerminalEvents builds the usage and done events that end a successful stream.
func (s *Stream) TerminalEvents(reason llm.StopReason) []llm.Event {
	return []llm.Event{s.UsageEvent(), s.Done(reason)}
}

// ReadFailure wraps a failed source read as a terminal failure.
func ReadFailure(prefix string, err error) Terminal {
	wrapped := fmt.Errorf("%s: read response stream: %w", prefix, err)
	message := prefix + ": model stream failed"
	if errors.Is(err, context.Canceled) {
		message = prefix + ": request canceled"
	}
	return Terminal{Err: wrapped, Message: message}
}

// UnexpectedEOF marks a cleanly ended source that produced no terminal event.
func UnexpectedEOF(prefix string) Terminal {
	return Terminal{
		Err: fmt.Errorf(
			"%s: response stream ended before a terminal event: %w",
			prefix,
			io.ErrUnexpectedEOF,
		),
		Message: prefix + ": model stream ended unexpectedly",
	}
}

func (s *Stream) shift() llm.Event {
	event := s.pending[0]
	s.pending = s.pending[1:]
	return event
}

func (s *Stream) errorEvent(err error, message string) llm.Event {
	s.finished = true
	reason := llm.StopReasonError
	if errors.Is(err, context.Canceled) {
		reason = llm.StopReasonAborted
	}
	snapshot := s.Snapshot(reason, message)
	return llm.Event{
		Type:       llm.EventTypeError,
		StopReason: reason,
		Message:    &snapshot,
		Err:        err,
	}
}
