//go:build integration && darwin

package app

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

// One serial acceptance Loop owns these samples. No arguments, window identity,
// model text, credentials or images are copied into the timing report.
type nativeModelTimings struct {
	Requests []*nativeModelRequestTiming `json:"requests"`
	Tools    []*nativeModelToolTiming    `json:"tools"`
	active   *nativeModelToolTiming
	turnEnd  time.Time
}

type nativeModelRequestTiming struct {
	Sequence        int      `json:"sequence"`
	PreparationMS   *float64 `json:"preparation_gap_ms,omitempty"`
	FirstToolCallMS *float64 `json:"first_tool_call_ms,omitempty"`
	FinishedMS      *float64 `json:"finished_ms,omitempty"`
	Terminal        bool     `json:"terminal"`
	started         time.Time
}

type nativeModelToolTiming struct {
	Sequence      int      `json:"sequence"`
	Request       int      `json:"request"`
	Name          string   `json:"name"`
	TotalMS       *float64 `json:"total_ms,omitempty"`
	GuardMS       *float64 `json:"guard_check_ms,omitempty"`
	ManagedCallMS *float64 `json:"managed_call_ms,omitempty"`
	RevalidateMS  *float64 `json:"guard_revalidate_ms,omitempty"`
	started       time.Time
}

func nativeMS(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func (m *nativeModelTimings) startRequest() *nativeModelRequestTiming {
	measurement := &nativeModelRequestTiming{Sequence: len(m.Requests) + 1, started: time.Now()}
	if !m.turnEnd.IsZero() {
		elapsed := nativeMS(measurement.started.Sub(m.turnEnd))
		measurement.PreparationMS = &elapsed
		m.turnEnd = time.Time{}
	}
	m.Requests = append(m.Requests, measurement)
	return measurement
}

func (m *nativeModelTimings) event(_ context.Context, event agent.AgentEvent) error {
	switch event.Type {
	case agent.EventTypeToolExecutionStart:
		name := "unregistered"
		switch event.ToolCall.Name {
		case "skill", "tool_search", "tool_result_read":
			name = event.ToolCall.Name
		}
		m.active = &nativeModelToolTiming{Sequence: len(m.Tools) + 1, Request: len(m.Requests), Name: name, started: time.Now()}
		m.Tools = append(m.Tools, m.active)
	case agent.EventTypeToolExecutionEnd:
		if m.active != nil {
			elapsed := nativeMS(time.Since(m.active.started))
			m.active.TotalMS = &elapsed
			m.active = nil
		}
	case agent.EventTypeTurnEnd:
		m.turnEnd = time.Now()
	}
	return nil
}

type nativeModelTimedStream struct {
	llm.Stream
	measurement *nativeModelRequestTiming
}

func (s *nativeModelTimedStream) Next() (llm.Event, error) {
	event, err := s.Stream.Next()
	if s.measurement.FinishedMS != nil {
		return event, err
	}
	elapsed := nativeMS(time.Since(s.measurement.started))
	if event.Type == llm.EventTypeToolCallEnd && event.ToolCall != nil && s.measurement.FirstToolCallMS == nil {
		s.measurement.FirstToolCallMS = &elapsed
	}
	if err != nil || event.Type == llm.EventTypeDone || event.Type == llm.EventTypeError {
		s.measurement.FinishedMS = &elapsed
		s.measurement.Terminal = err == nil && (event.Type == llm.EventTypeDone || event.Type == llm.EventTypeError)
	}
	return event, err
}

func (m *nativeModelTimings) verify(t *testing.T, requests, tools int) {
	t.Helper()
	if len(m.Requests) != requests || len(m.Tools) != tools || m.active != nil {
		t.Fatal("timing coverage does not match completed model/tool calls")
	}
	for i, request := range m.Requests {
		if request.Sequence != i+1 || request.FinishedMS == nil || *request.FinishedMS < 0 {
			t.Fatal("missing settled model request timing")
		}
		if request.FirstToolCallMS != nil && (*request.FirstToolCallMS < 0 || *request.FirstToolCallMS > *request.FinishedMS) {
			t.Fatal("model action output timing exceeds request duration")
		}
		if request.PreparationMS != nil && *request.PreparationMS < 0 {
			t.Fatal("negative request preparation gap")
		}
		t.Logf("request=%d model_ms=%.3f terminal=%v", request.Sequence, *request.FinishedMS, request.Terminal)
	}
	for i, call := range m.Tools {
		if call.Sequence != i+1 || call.Request < 1 || call.Request > requests || call.TotalMS == nil || call.GuardMS == nil || *call.GuardMS < 0 || *call.GuardMS > *call.TotalMS {
			t.Fatal("missing tool/Guard timing or invalid boundaries")
		}
		if call.ManagedCallMS != nil && (*call.ManagedCallMS < 0 || *call.ManagedCallMS > *call.TotalMS) {
			t.Fatal("managed backend timing exceeds tool duration")
		}
	}
}

// Timing must not replace stream events, turn transport failure into completion,
// swallow Close errors, or overwrite the first measured terminal boundary.
func TestNativeModelTimingStreamPreservesProtocol(t *testing.T) {
	t.Parallel()
	for _, terminal := range []bool{false, true} {
		events := []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeToolCallEnd, ToolCall: &llm.ToolCall{ID: "synthetic", Name: "tool_search"}}}
		if terminal {
			events = append(events, llm.Event{Type: llm.EventTypeDone})
		}
		closeErr := errors.New("synthetic close failure")
		source := &nativeTimingCloseStream{Stream: &eventStream{events: events}, err: closeErr}
		record := &nativeModelRequestTiming{started: time.Now()}
		measured := &nativeModelTimedStream{Stream: source, measurement: record}
		for _, want := range events {
			got, err := measured.Next()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("measurement changed a stream event", err)
			}
		}
		if _, err := measured.Next(); !errors.Is(err, io.EOF) || record.Terminal != terminal || record.FinishedMS == nil || record.FirstToolCallMS == nil {
			t.Fatal("measurement lost error/completion distinction or timing", err)
		}
		finished := *record.FinishedMS
		if _, err := measured.Next(); !errors.Is(err, io.EOF) || *record.FinishedMS != finished {
			t.Fatal("measurement changed the settled stream boundary", err)
		}
		if err := measured.Close(); !errors.Is(err, closeErr) || source.closes != 1 {
			t.Fatal("measurement changed stream cleanup", err)
		}
	}
}

type nativeTimingCloseStream struct {
	llm.Stream
	err    error
	closes int
}

func (s *nativeTimingCloseStream) Close() error {
	s.closes++
	return errors.Join(s.Stream.Close(), s.err)
}
