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
	Sequence     int                      `json:"sequence"`
	Request      int                      `json:"request"`
	Name         string                   `json:"name"`
	TotalMS      *float64                 `json:"total_ms,omitempty"`
	GuardMS      *float64                 `json:"guard_check_ms,omitempty"`
	RevalidateMS *float64                 `json:"guard_revalidate_ms,omitempty"`
	Action       *nativeModelActionTiming `json:"action,omitempty"`
	started      time.Time
}

type nativeModelActionTiming struct {
	Kind            string  `json:"kind"`
	TotalMS         float64 `json:"total_ms"`
	QueueMS         float64 `json:"queue_ms"`
	DriverMS        float64 `json:"driver_round_trip_ms"`
	ConditionWaitMS float64 `json:"condition_wait_ms"`
	ObservationMS   float64 `json:"observation_ms"`
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
		case "desktop_apps", "desktop_observe", "desktop_act":
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
		if call.Action != nil {
			action := call.Action
			localMS := *call.GuardMS
			if call.RevalidateMS != nil {
				localMS += *call.RevalidateMS
			}
			if action.TotalMS <= 0 || action.TotalMS+localMS > *call.TotalMS || action.QueueMS+action.DriverMS+action.ConditionWaitMS+action.ObservationMS > action.TotalMS {
				t.Fatal("native action phase timings exceed their owning operation")
			}
			t.Logf("tool=%d action=%s total_ms=%.3f guard_ms=%.3f queue_ms=%.3f driver_ms=%.3f wait_ms=%.3f observation_ms=%.3f", call.Sequence, action.Kind, *call.TotalMS, *call.GuardMS, action.QueueMS, action.DriverMS, action.ConditionWaitMS, action.ObservationMS)
		}
	}
}

// Timing must not replace stream events, turn transport failure into completion,
// swallow Close errors, or overwrite the first measured terminal boundary.
func TestNativeModelTimingStreamPreservesProtocol(t *testing.T) {
	t.Parallel()
	for _, terminal := range []bool{false, true} {
		events := []llm.Event{{Type: llm.EventTypeStart}, {Type: llm.EventTypeToolCallEnd, ToolCall: &llm.ToolCall{ID: "synthetic", Name: "desktop_apps"}}}
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
