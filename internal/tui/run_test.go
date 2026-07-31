package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
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
		{
			name:   "missing model",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, string, agent.AgentEventSink) error { return nil }),
			options: Options{
				Input:  emptyReader{},
				Output: io.Discard,
			},
			want: "model ID is required",
		},
		{
			name:   "missing working directory",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, string, agent.AgentEventSink) error { return nil }),
			options: Options{
				Input:  emptyReader{},
				Output: io.Discard,
				Model:  llm.Model{ID: "test"},
			},
			want: "working directory is required",
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

func TestServeRunsExecutesSlashCommandsThroughRunner(t *testing.T) {
	t.Parallel()

	runner := &slashRunner{
		runnerFunc: func(
			context.Context,
			string,
			agent.AgentEventSink,
		) error {
			t.Fatal("slash command executed as an agent prompt")
			return nil
		},
		runCommand: func(
			_ context.Context,
			request SlashCommandRequest,
		) (string, error) {
			if request != (SlashCommandRequest{Name: "tree"}) {
				t.Fatalf("slash command request = %#v, want tree", request)
			}
			return "Session tree", nil
		},
		state: RuntimeState{
			Model:    llm.Model{ID: "selected-model"},
			Thinking: llm.ThinkingLevelHigh,
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveRuns(ctx, runner, requests, done)

	updates := make(chan runUpdate, runUpdateBuffer)
	command := SlashCommandRequest{Name: "tree"}
	requests <- runRequest{command: &command, updates: updates}

	start := receiveRunUpdate(t, updates)
	if start.cancel == nil {
		t.Fatal("first slash command update has no cancellation function")
	}
	terminal := receiveRunUpdate(t, updates)
	if !terminal.done ||
		terminal.err != nil ||
		terminal.output != "Session tree" ||
		terminal.state == nil ||
		terminal.state.Model.ID != "selected-model" ||
		terminal.state.Thinking != llm.ThinkingLevelHigh ||
		terminal.commands == nil ||
		len(*terminal.commands) != 1 ||
		(*terminal.commands)[0].Name != "tree" {
		t.Fatalf("terminal slash command update = %#v", terminal)
	}
	if _, open := <-updates; open {
		t.Fatal("slash command update channel remains open after completion")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run controller did not stop after slash command")
	}
}

func TestServeRunsRefreshesSlashCommandMenusAfterPrompt(t *testing.T) {
	t.Parallel()

	runner := &slashRunner{
		runnerFunc: func(
			context.Context,
			string,
			agent.AgentEventSink,
		) error {
			return nil
		},
		runCommand: func(
			context.Context,
			SlashCommandRequest,
		) (string, error) {
			t.Fatal("prompt executed as a slash command")
			return "", nil
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveRuns(ctx, runner, requests, done)

	updates := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "inspect", updates: updates}
	_ = receiveRunUpdate(t, updates)
	terminal := receiveRunUpdate(t, updates)
	if !terminal.done ||
		terminal.commands == nil ||
		len(*terminal.commands) != 1 ||
		(*terminal.commands)[0].Name != "tree" {
		t.Fatalf("terminal prompt update = %#v, want refreshed commands", terminal)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run controller did not stop after prompt")
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

type slashRunner struct {
	runnerFunc
	runCommand func(
		context.Context,
		SlashCommandRequest,
	) (string, error)
	state RuntimeState
}

func (r *slashRunner) SlashCommands() []SlashCommand {
	return []SlashCommand{{Name: "tree", Description: "Show Session tree"}}
}

func (r *slashRunner) RunSlashCommand(
	ctx context.Context,
	request SlashCommandRequest,
) (string, error) {
	return r.runCommand(ctx, request)
}

func (r *slashRunner) RuntimeState() RuntimeState {
	return r.state
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) {
	return 0, errors.New("unused")
}
