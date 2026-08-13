package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
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
			runner: runnerFunc(func(context.Context, RunInput, DisplayEventSink) error { return nil }),
			want:   "input is required",
		},
		{
			name:   "nil output",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, RunInput, DisplayEventSink) error { return nil }),
			options: Options{
				Input: emptyReader{},
			},
			want: "output is required",
		},
		{
			name:   "missing model",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, RunInput, DisplayEventSink) error { return nil }),
			options: Options{
				Input:  emptyReader{},
				Output: io.Discard,
			},
			want: "model ID is required",
		},
		{
			name:   "missing working directory",
			ctx:    t.Context(),
			runner: runnerFunc(func(context.Context, RunInput, DisplayEventSink) error { return nil }),
			options: Options{
				Input:  emptyReader{},
				Output: io.Discard,
				Model:  DisplayModel{ID: "test"},
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

	runner := runnerFunc(func(ctx context.Context, input RunInput, sink DisplayEventSink) error {
		if input.Prompt != "inspect" {
			return errors.New("unexpected prompt")
		}
		return sink(ctx, DisplayEvent{Kind: DisplayEventAgentEnd})
	})
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveRuns(ctx, runner, requests, done)

	updates := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "inspect", updates: updates}

	start := receiveRunUpdate(t, updates)
	if start.cancel == nil || start.active == nil {
		t.Fatal("first run update has no active run or cancellation function")
	}
	event := receiveRunUpdate(t, updates)
	if event.event.Kind != DisplayEventAgentEnd {
		t.Errorf("event kind = %d, want %d", event.event.Kind, DisplayEventAgentEnd)
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
			RunInput,
			DisplayEventSink,
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
			Model:    DisplayModel{ID: "selected-model"},
			Thinking: DisplayThinkingHigh,
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
		terminal.state.Thinking != DisplayThinkingHigh ||
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

func TestServeSideRunsWhileMainRunIsBlocked(t *testing.T) {
	t.Parallel()

	mainStarted := make(chan struct{})
	mainRelease := make(chan struct{})
	mainRunner := runnerFunc(func(
		ctx context.Context,
		_ RunInput,
		_ DisplayEventSink,
	) error {
		close(mainStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-mainRelease:
			return nil
		}
	})
	manager := newFakeSideManager()
	manager.newRunner = func(_ uint64) Runner {
		return runnerFunc(func(
			ctx context.Context,
			input RunInput,
			sink DisplayEventSink,
		) error {
			if input.Prompt != "quick question" {
				t.Fatalf("side prompt = %q, want quick question", input.Prompt)
			}
			return sink(ctx, DisplayEvent{Kind: DisplayEventAgentEnd})
		})
	}

	ctx, cancel := context.WithCancel(t.Context())
	mainRequests := make(chan runRequest)
	sideRequests := make(chan runRequest)
	mainDone := make(chan struct{})
	sideDone := make(chan struct{})
	go serveRuns(ctx, mainRunner, mainRequests, mainDone)
	go serveSideRuns(ctx, manager, sideRequests, sideDone)

	mainUpdates := make(chan runUpdate, runUpdateBuffer)
	mainRequests <- runRequest{prompt: "long task", updates: mainUpdates}
	mainStart := receiveRunUpdate(t, mainUpdates)
	if mainStart.active == nil || mainStart.cancel == nil {
		t.Fatalf("main start update = %#v", mainStart)
	}
	select {
	case <-mainStarted:
	case <-time.After(time.Second):
		t.Fatal("main run did not start")
	}

	sideUpdates := make(chan runUpdate, runUpdateBuffer)
	sideRequests <- runRequest{
		prompt:     "quick question",
		updates:    sideUpdates,
		sideCreate: true,
	}
	sideStart := receiveRunUpdate(t, sideUpdates)
	if sideStart.active == nil || sideStart.cancel == nil ||
		sideStart.sideThread == nil {
		t.Fatalf("side start update = %#v", sideStart)
	}
	sideEvent := receiveRunUpdate(t, sideUpdates)
	if sideEvent.event.Kind != DisplayEventAgentEnd {
		t.Fatalf("side event = %#v, want agent end", sideEvent)
	}
	sideTerminal := receiveRunUpdate(t, sideUpdates)
	if !sideTerminal.done || sideTerminal.err != nil {
		t.Fatalf("side terminal update = %#v", sideTerminal)
	}
	if got := manager.createCalls; got != 1 {
		t.Fatalf("CreateSideThread() calls = %d, want 1", got)
	}

	select {
	case update := <-mainUpdates:
		t.Fatalf("blocked main run completed during side run: %#v", update)
	default:
	}
	close(mainRelease)
	mainTerminal := receiveRunUpdate(t, mainUpdates)
	if !mainTerminal.done || mainTerminal.err != nil {
		t.Fatalf("main terminal update = %#v", mainTerminal)
	}

	cancel()
	for name, done := range map[string]<-chan struct{}{
		"main": mainDone,
		"side": sideDone,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s controller did not stop", name)
		}
	}
}

func TestServeSideRunsRunsThreadsConcurrently(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	manager := newFakeSideManager()
	manager.newRunner = func(_ uint64) Runner {
		return runnerFunc(func(
			ctx context.Context,
			_ RunInput,
			_ DisplayEventSink,
		) error {
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		})
	}

	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, manager, requests, done)

	first := make(chan runUpdate, runUpdateBuffer)
	second := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "one", updates: first, sideCreate: true}
	requests <- runRequest{prompt: "two", updates: second, sideCreate: true}

	firstStart := receiveRunUpdate(t, first)
	secondStart := receiveRunUpdate(t, second)
	if firstStart.sideThread == nil || secondStart.sideThread == nil {
		t.Fatalf("side runs missing thread metadata: %#v / %#v", firstStart, secondStart)
	}
	if firstStart.sideThread.ID == secondStart.sideThread.ID {
		t.Fatalf("both runs resolved to thread %d", firstStart.sideThread.ID)
	}
	// Both runners must actually be in flight before either can finish.
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("side runner did not start")
		}
	}
	close(release)
	if terminal := drainRunUpdate(t, first); !terminal.done || terminal.err != nil {
		t.Fatalf("first terminal update = %#v", terminal)
	}
	if terminal := drainRunUpdate(t, second); !terminal.done || terminal.err != nil {
		t.Fatalf("second terminal update = %#v", terminal)
	}
	if got := manager.createCalls; got != 2 {
		t.Fatalf("CreateSideThread() calls = %d, want 2", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("side controller did not stop")
	}
}

func TestServeSideRunsFollowUpOpensExistingThread(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.newRunner = func(_ uint64) Runner {
		return runnerFunc(func(
			ctx context.Context,
			_ RunInput,
			sink DisplayEventSink,
		) error {
			return sink(ctx, DisplayEvent{Kind: DisplayEventAgentEnd})
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, manager, requests, done)

	first := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "first", updates: first, sideCreate: true}
	if terminal := drainRunUpdate(t, first); !terminal.done || terminal.err != nil {
		t.Fatalf("create terminal update = %#v", terminal)
	}

	second := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{
		prompt:       "second",
		updates:      second,
		sideCreate:   false,
		sideThreadID: 1,
	}
	secondStart := receiveRunUpdate(t, second)
	if secondStart.sideThread == nil || secondStart.sideThread.ID != 1 {
		t.Fatalf("follow-up start update = %#v, want thread 1", secondStart)
	}
	if terminal := drainRunUpdate(t, second); !terminal.done || terminal.err != nil {
		t.Fatalf("follow-up terminal update = %#v", terminal)
	}
	if len(manager.openIDs) != 1 || manager.openIDs[0] != 1 {
		t.Fatalf("OpenSideThread() ids = %#v, want [1]", manager.openIDs)
	}
	if got := manager.createCalls; got != 1 {
		t.Fatalf("CreateSideThread() calls = %d, want 1", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("side controller did not stop")
	}
}

func TestServeSideRunsDeliversRunStartRejection(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.newRunner = func(_ uint64) Runner {
		return runnerFunc(func(
			context.Context,
			RunInput,
			DisplayEventSink,
		) error {
			return interaction.ErrSideThreadConcurrencyLimit
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, manager, requests, done)

	updates := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "question", updates: updates, sideCreate: true}
	terminal := drainRunUpdate(t, updates)
	if !terminal.done ||
		!errors.Is(terminal.err, interaction.ErrSideThreadConcurrencyLimit) {
		t.Fatalf("rejection terminal update = %#v", terminal)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("side controller did not stop")
	}
}

func TestServeSideRunsStopsBlockedRunsOnCancellation(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.newRunner = func(_ uint64) Runner {
		return runnerFunc(func(
			ctx context.Context,
			_ RunInput,
			_ DisplayEventSink,
		) error {
			<-ctx.Done()
			return ctx.Err()
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, manager, requests, done)

	for index := 0; index < 2; index++ {
		updates := make(chan runUpdate, runUpdateBuffer)
		requests <- runRequest{
			prompt:     "question",
			updates:    updates,
			sideCreate: true,
		}
		_ = receiveRunUpdate(t, updates)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("side controller leaked blocked runs on shutdown")
	}
}

func TestServeRunsRefreshesSlashCommandMenusAfterPrompt(t *testing.T) {
	t.Parallel()

	runner := &slashRunner{
		runnerFunc: func(
			context.Context,
			RunInput,
			DisplayEventSink,
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

// drainRunUpdate receives updates until the terminal one and returns it.
func drainRunUpdate(t *testing.T, updates <-chan runUpdate) runUpdate {
	t.Helper()
	terminal := receiveRunUpdate(t, updates)
	for !terminal.done {
		terminal = receiveRunUpdate(t, updates)
	}
	return terminal
}

type runnerFunc func(context.Context, RunInput, DisplayEventSink) error

func (f runnerFunc) NewRun(
	input RunInput,
	sink DisplayEventSink,
) (ActiveRun, error) {
	return &activeRunFunc{
		run: func(ctx context.Context) error {
			return f(ctx, input, sink)
		},
	}, nil
}

type activeRunFunc struct {
	run     func(context.Context) error
	deliver func(interaction.Delivery) error
}

func (r *activeRunFunc) Run(ctx context.Context) error {
	if r.run == nil {
		return nil
	}
	return r.run(ctx)
}

func (r *activeRunFunc) Deliver(delivery interaction.Delivery) error {
	if r.deliver == nil {
		return nil
	}
	return r.deliver(delivery)
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
