package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
)

func TestRunRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     context.Context
		runner  Runner
		options Options
		want    string
	}{
		{
			name: "nil context",
			want: "context is required",
		},
		{
			name: "nil runner",
			ctx:  t.Context(),
			want: "runner is required",
		},
		{
			name:   "nil input",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, string, agent.AgentEventSink) error { return nil }),
			want:   "input is required",
		},
		{
			name:   "nil output",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, string, agent.AgentEventSink) error { return nil }),
			options: Options{
				Input: emptyReader{},
			},
			want: "output is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Run(tt.ctx, tt.runner, tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run() error = %v, want text %q", err, tt.want)
			}
		})
	}
}

func TestServeRunsOwnsPerRunEventChannel(t *testing.T) {
	t.Parallel()

	runner := runnerFunc(func(ctx context.Context, prompt string, sink agent.AgentEventSink) error {
		if prompt != "inspect" {
			return errors.New("unexpected prompt")
		}
		return sink(ctx, agent.AgentEvent{Type: agent.EventTypeAgentStart})
	})
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveRuns(ctx, runner, requests, done)

	updates := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "inspect", updates: updates}

	start := receiveRunUpdate(t, updates)
	if start.cancel == nil {
		t.Fatal("first run update has no cancellation function")
	}
	event := receiveRunUpdate(t, updates)
	if event.event.Type != agent.EventTypeAgentStart {
		t.Errorf("event type = %q, want %q", event.event.Type, agent.EventTypeAgentStart)
	}
	terminal := receiveRunUpdate(t, updates)
	if !terminal.done || terminal.err != nil {
		t.Errorf("terminal update = %#v, want successful completion", terminal)
	}
	if _, open := <-updates; open {
		t.Fatal("run update channel remains open after completion")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run controller did not stop after cancellation")
	}
}

func receiveRunUpdate(t *testing.T, updates <-chan runUpdate) runUpdate {
	t.Helper()
	select {
	case update, open := <-updates:
		if !open {
			t.Fatal("run update channel closed early")
		}
		return update
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run update")
		return runUpdate{}
	}
}

type runnerFunc func(context.Context, string, agent.AgentEventSink) error

func (f runnerFunc) Run(
	ctx context.Context,
	prompt string,
	sink agent.AgentEventSink,
) error {
	return f(ctx, prompt, sink)
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) {
	return 0, errors.New("unused")
}
