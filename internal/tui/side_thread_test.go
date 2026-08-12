package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestModelRoutesBTWBesideActiveMainRun(t *testing.T) {
	t.Parallel()

	mainRequests := make(chan runRequest, 1)
	mainDone := make(chan struct{})
	sideRequests := make(chan runRequest, 1)
	sideDone := make(chan struct{})
	current := newModel(mainRequests, mainDone, btwSlashCommand())
	current.sideRequests = sideRequests
	current.sideControllerDone = sideDone
	current.running = true
	current.acceptsDelivery = true
	mainDeliveries := 0
	current.activeRun = &activeRunFunc{deliver: func(interaction.Delivery) error {
		mainDeliveries++
		return errors.New("BTW reached the main mailbox")
	}}
	current.input.SetValue("/btw explain the current plan")

	updated, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command == nil {
		t.Fatal("enter did not start a side question")
	}
	if mainDeliveries != 0 {
		t.Fatalf("main deliveries = %d, want 0", mainDeliveries)
	}
	if !updated.running || !updated.side.isRunning || !updated.side.isVisible {
		t.Fatalf("main/side state = %#v", updated.side)
	}
	if len(updated.entries) != 0 {
		t.Fatalf("main transcript entries = %#v, want none", updated.entries)
	}
	if got := updated.side.entries[0].question; got != "explain the current plan" {
		t.Fatalf("side question = %q", got)
	}

	message := command()
	started, ok := message.(sideRunStartedMsg)
	if !ok || started.updates == nil {
		t.Fatalf("start command message = %#v, want sideRunStartedMsg", message)
	}
	request := <-sideRequests
	if request.prompt != "explain the current plan" {
		t.Fatalf("side request prompt = %q", request.prompt)
	}
	select {
	case request := <-mainRequests:
		t.Fatalf("BTW emitted main request %#v", request)
	default:
	}
}

func TestModelRoutesBTWWhileMainRunIsStarting(t *testing.T) {
	t.Parallel()

	sideRequests := make(chan runRequest, 1)
	current := newModel(
		make(chan runRequest, 1),
		make(chan struct{}),
		btwSlashCommand(),
	)
	current.sideRequests = sideRequests
	current.sideControllerDone = make(chan struct{})
	current.running = true
	current.acceptsDelivery = false
	current.input.SetValue("/btw what context is available?")

	updated, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command == nil || !updated.side.isRunning {
		t.Fatalf("BTW during main startup = %#v, command %v", updated.side, command)
	}
	_ = command()
	if request := <-sideRequests; request.prompt != "what context is available?" {
		t.Fatalf("side request prompt = %q", request.prompt)
	}
}

func TestModelBareBTWOpensAndReopensWithoutStartingRun(t *testing.T) {
	t.Parallel()

	sideRequests := make(chan runRequest, 1)
	current := newModel(
		make(chan runRequest, 1),
		make(chan struct{}),
		btwSlashCommand(),
	)
	current.sideRequests = sideRequests
	current.sideControllerDone = make(chan struct{})
	current.side.entries = []sideThreadEntry{{
		question: "earlier question",
		answer:   "earlier answer",
		complete: true,
	}}
	current.input.SetValue("/btw")

	opened, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command != nil {
		t.Fatal("bare /btw started a run")
	}
	if !opened.side.isVisible || opened.side.isRunning {
		t.Fatalf("side state = %#v", opened.side)
	}
	if got := ansi.Strip(opened.transcriptView()); !strings.Contains(got, "earlier answer") {
		t.Fatalf("reopened side transcript = %q", got)
	}
	select {
	case request := <-sideRequests:
		t.Fatalf("bare /btw emitted request %#v", request)
	default:
	}
}

func TestModelSideComposerDoesNotRecallMainPromptHistory(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.side.isVisible = true
	current.promptHistory = []string{"main prompt"}
	current.input.SetValue("side draft")
	updated := updateModel(t, current, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := updated.input.Value(); got != "side draft" {
		t.Fatalf("side draft after up = %q, want unchanged", got)
	}
	if updated.historyIndex != -1 {
		t.Fatalf("main history index = %d, want -1", updated.historyIndex)
	}
}

func TestModelSidePanelOwnsCancelAndCloseKeys(t *testing.T) {
	t.Parallel()

	t.Run("control c cancels only side", func(t *testing.T) {
		t.Parallel()

		current := newModel(make(chan runRequest), make(chan struct{}))
		current.running = true
		current.acceptsDelivery = true
		current.side.isVisible = true
		current.side.isRunning = true
		mainCancelled := false
		sideCancelled := false
		current.cancelRun = func() { mainCancelled = true }
		current.side.cancel = func() { sideCancelled = true }

		updated, command, handled := current.handleKey(tea.KeyPressMsg{
			Code: 'c',
			Mod:  tea.ModCtrl,
		})
		if !handled || command != nil {
			t.Fatal("ctrl+c did not stay inside the side panel")
		}
		if !sideCancelled || mainCancelled {
			t.Fatalf(
				"cancellation: side=%v main=%v",
				sideCancelled,
				mainCancelled,
			)
		}
		if !updated.side.isVisible || !updated.side.isRunning {
			t.Fatalf("side state after cancel request = %#v", updated.side)
		}
	})

	t.Run("escape closes without cancelling either run", func(t *testing.T) {
		t.Parallel()

		current := newModel(make(chan runRequest), make(chan struct{}))
		current.running = true
		current.acceptsDelivery = true
		current.side.isVisible = true
		current.side.isRunning = true
		mainCancelled := false
		sideCancelled := false
		current.cancelRun = func() { mainCancelled = true }
		current.side.cancel = func() { sideCancelled = true }

		updated, command, handled := current.handleKey(tea.KeyPressMsg{
			Code: tea.KeyEscape,
		})
		if !handled || command != nil {
			t.Fatal("escape did not close the side panel synchronously")
		}
		if updated.side.isVisible || !updated.side.isRunning {
			t.Fatalf("side state after close = %#v", updated.side)
		}
		if sideCancelled || mainCancelled {
			t.Fatalf(
				"escape cancelled a run: side=%v main=%v",
				sideCancelled,
				mainCancelled,
			)
		}
	})
}

func TestModelQueuesEarlySideCancellation(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.side.isVisible = true
	current.side.isRunning = true
	updated, _, handled := current.handleKey(tea.KeyPressMsg{
		Code: 'c',
		Mod:  tea.ModCtrl,
	})
	if !handled || !updated.side.cancelPending {
		t.Fatalf("early cancellation state = %#v", updated.side)
	}

	updates := make(chan runUpdate)
	updated.side.updates = updates
	cancelled := false
	modelValue, _ := updated.applySideRunBatch(sideRunBatchMsg{
		source: updates,
		updates: []runUpdate{{
			cancel: func() { cancelled = true },
		}},
	})
	updated = modelValue.(model)
	if !cancelled || updated.side.status != "Cancelling side answer..." {
		t.Fatalf("queued cancellation: called=%v state=%#v", cancelled, updated.side)
	}
}

func TestModelClosedSidePanelKeepsReceivingCompletion(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.side.isVisible = true
	current.side.isRunning = true
	current.side.assistantEntry = 0
	current.side.entries = []sideThreadEntry{{question: "question"}}
	updates := make(chan runUpdate)
	current.side.updates = updates
	closed, _, _ := current.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	modelValue, _ := closed.applySideRunBatch(sideRunBatchMsg{
		source:  updates,
		updates: []runUpdate{{done: true}},
		closed:  true,
	})
	closed = modelValue.(model)
	if closed.side.isVisible || closed.side.isRunning {
		t.Fatalf("hidden completion state = %#v", closed.side)
	}
	if closed.side.entries[0].complete != true {
		t.Fatalf("hidden side entry = %#v, want complete", closed.side.entries[0])
	}
}

func TestModelAppliesSideAndMainUpdatesIndependently(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 80, Height: 24})
	current.running = true
	current.side.isVisible = true
	current.side.isRunning = true
	current.side.assistantEntry = 0
	current.side.entries = []sideThreadEntry{{question: "what changed?"}}

	updated := updateModel(t, current, sideRunBatchMsg{
		updates: []runUpdate{
			{cancel: func() {}},
			{event: DisplayEvent{Kind: DisplayEventAssistantStart}},
			{event: DisplayEvent{
				Kind: DisplayEventAssistantDelta,
				Delta: DisplayDelta{
					Kind:  DisplayDeltaText,
					Delta: "A private answer",
				},
			}},
			{event: DisplayEvent{
				Kind: DisplayEventAssistantEnd,
				Assistant: AssistantDisplay{
					Text:      "**A private answer**",
					Concludes: true,
				},
			}},
			{done: true},
		},
		closed: true,
	})
	if updated.side.isRunning || !updated.side.isVisible {
		t.Fatalf("completed side state = %#v", updated.side)
	}
	if !updated.running {
		t.Fatal("side completion stopped the main run")
	}
	header := ansi.Strip(updated.headerView(80))
	for _, want := range []string{"BTW", "MAIN WORKING"} {
		if !strings.Contains(header, want) {
			t.Errorf("side header = %q, want %q", header, want)
		}
	}
	view := ansi.Strip(updated.transcriptView())
	for _, want := range []string{
		"BTW SIDE THREAD",
		"what changed?",
		"A private answer",
		"excluded from the main Session",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("side transcript = %q, want %q", view, want)
		}
	}
	if len(updated.entries) != 0 {
		t.Fatalf("side answer leaked into main entries: %#v", updated.entries)
	}

	updated = updateModel(t, updated, runBatchMsg{updates: []runUpdate{{
		done: true,
	}}})
	if updated.running {
		t.Fatal("main terminal update was not applied behind side panel")
	}
	if !updated.side.isVisible {
		t.Fatal("main completion closed the side panel")
	}
}

func TestModelSideComposerNormalizesBTWPrefix(t *testing.T) {
	t.Parallel()

	sideRequests := make(chan runRequest, 1)
	current := newModel(make(chan runRequest), make(chan struct{}))
	current.sideRequests = sideRequests
	current.sideControllerDone = make(chan struct{})
	current.side.isVisible = true
	current.input.SetValue("/btw normalized question")
	updated, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command == nil {
		t.Fatal("prefixed side question did not start")
	}
	if got := updated.side.entries[0].question; got != "normalized question" {
		t.Fatalf("normalized side question = %q", got)
	}
	_ = command()
	if request := <-sideRequests; request.prompt != "normalized question" {
		t.Fatalf("side request prompt = %q", request.prompt)
	}
}

func TestModelSideFollowUpStartsAnotherIndependentRun(t *testing.T) {
	t.Parallel()

	sideRequests := make(chan runRequest, 1)
	current := newModel(make(chan runRequest), make(chan struct{}))
	current.sideRequests = sideRequests
	current.sideControllerDone = make(chan struct{})
	current.side.isVisible = true
	current.side.entries = []sideThreadEntry{{
		question: "first question",
		answer:   "first answer",
		complete: true,
	}}
	current.input.Placeholder = sidePlaceholder
	current.input.SetValue("second question")

	updated, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command == nil {
		t.Fatal("side follow-up did not start another run")
	}
	if len(updated.side.entries) != 2 ||
		updated.side.entries[1].question != "second question" {
		t.Fatalf("side entries = %#v", updated.side.entries)
	}
	if !updated.side.isRunning {
		t.Fatal("side follow-up did not enter running state")
	}
	_ = command()
	request := <-sideRequests
	if request.prompt != "second question" {
		t.Fatalf("side follow-up prompt = %q", request.prompt)
	}
}

func TestModelIgnoresClosedChannelFromPreviousSideRun(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	previous := make(chan runUpdate)
	current.side.updates = previous
	current.side.isRunning = true
	current.side.assistantEntry = 0
	current.side.entries = []sideThreadEntry{{question: "first"}}

	terminal, _ := current.applySideRunBatch(sideRunBatchMsg{
		source:  previous,
		updates: []runUpdate{{done: true}},
	})
	current = terminal.(model)
	if current.side.isRunning {
		t.Fatal("first side run did not finish")
	}

	next := make(chan runUpdate)
	current.side.updates = next
	current.side.isRunning = true
	current.side.assistantEntry = 1
	current.side.entries = append(
		current.side.entries,
		sideThreadEntry{question: "second"},
	)
	stale, command := current.applySideRunBatch(sideRunBatchMsg{
		source: previous,
		closed: true,
	})
	current = stale.(model)
	if command != nil {
		t.Fatal("stale side batch returned a command")
	}
	if !current.side.isRunning || current.side.updates != next {
		t.Fatalf("stale close changed current side run: %#v", current.side)
	}
}

func TestModelCommandRefreshRetainsBTWCapability(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.sideRequests = make(chan runRequest)
	updated := updateModel(t, current, runBatchMsg{updates: []runUpdate{{
		commands: &[]SlashCommand{{Name: "tree", Description: "Show tree"}},
	}}})
	if _, exists := findSlashCommand(updated.commands, "btw"); !exists {
		t.Fatalf("refreshed commands omitted /btw: %#v", updated.commands)
	}
	if _, exists := findSlashCommand(updated.commands, "tree"); !exists {
		t.Fatalf("refreshed commands omitted /tree: %#v", updated.commands)
	}
}

func TestModelClosedSideControllerKeepsQuestionAsDraft(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.sideRequests = make(chan runRequest)
	current.sideControllerDone = make(chan struct{})
	current.side.isVisible = true
	current.side.isClosed = true
	current.input.SetValue("unsent question")
	updated, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled || command != nil {
		t.Fatal("closed side controller started a command")
	}
	if len(updated.side.entries) != 0 || updated.input.Value() != "unsent question" {
		t.Fatalf("closed side state = %#v, input %q", updated.side, updated.input.Value())
	}
	if !strings.Contains(updated.sideThreadView(), "controller is unavailable") {
		t.Fatalf("closed side view = %q", ansi.Strip(updated.sideThreadView()))
	}
}

func TestSideThreadViewFitsNarrowTerminal(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  minimumWidth,
		Height: 12,
	})
	current.side.isVisible = true
	current.side.entries = []sideThreadEntry{{
		question: "a long side question that wraps on a narrow terminal",
		answer:   "a concise answer that also wraps",
		complete: true,
	}}
	current.resizeLayout()
	if got := current.viewport.Width(); got != minimumWidth {
		t.Fatalf("side viewport width = %d, want %d", got, minimumWidth)
	}
	if view := current.View().Content; strings.TrimSpace(view) == "" {
		t.Fatal("narrow side view is empty")
	}
}

func TestServeSideRunsReportsFactoryFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("side factory failed")
	factory := &sideManagerRunner{createErr: boom}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, factory, requests, done)

	updates := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "question", updates: updates}
	terminal := receiveRunUpdate(t, updates)
	if !terminal.done || !errors.Is(terminal.err, boom) {
		t.Fatalf("side create terminal update = %#v", terminal)
	}
	select {
	case _, open := <-updates:
		if open {
			t.Fatal("factory failure update channel remains open")
		}
	case <-time.After(time.Second):
		t.Fatal("factory failure update channel did not close")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("side controller did not stop")
	}
}

func TestServeSideRunsCreatesFreshThreadAfterExpiredOpen(t *testing.T) {
	t.Parallel()

	manager := &sideManagerRunner{
		side: runnerFunc(func(
			ctx context.Context,
			_ RunInput,
			sink DisplayEventSink,
		) error {
			return sink(ctx, DisplayEvent{Kind: DisplayEventAgentEnd})
		}),
		openErr: interaction.ErrSideThreadNotFound,
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, manager, requests, done)

	first := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "first question", updates: first}
	if terminal := drainRunUpdate(t, first); !terminal.done || terminal.err != nil {
		t.Fatalf("first terminal update = %#v", terminal)
	}
	if manager.createCalls != 1 || manager.openCalls != 0 {
		t.Fatalf(
			"after first question: create=%d open=%d, want 1/0",
			manager.createCalls,
			manager.openCalls,
		)
	}

	// The thread expired between questions: the open fails and the
	// controller forgets the dead id.
	second := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "second question", updates: second}
	if terminal := drainRunUpdate(t, second); !terminal.done ||
		!errors.Is(terminal.err, interaction.ErrSideThreadNotFound) {
		t.Fatalf("second terminal update = %#v", terminal)
	}
	if manager.createCalls != 1 || manager.openCalls != 1 {
		t.Fatalf(
			"after expired open: create=%d open=%d, want 1/1",
			manager.createCalls,
			manager.openCalls,
		)
	}

	// The next question starts a fresh thread instead of reopening the dead
	// id again.
	third := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "third question", updates: third}
	if terminal := drainRunUpdate(t, third); !terminal.done || terminal.err != nil {
		t.Fatalf("third terminal update = %#v", terminal)
	}
	if manager.createCalls != 2 || manager.openCalls != 1 {
		t.Fatalf(
			"after fresh thread: create=%d open=%d, want 2/1",
			manager.createCalls,
			manager.openCalls,
		)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("side controller did not stop")
	}
}

func TestServeSideRunsStartsFreshThreadAfterReadOnlyRun(t *testing.T) {
	t.Parallel()

	// The first answer succeeds; the second run fails at start with the
	// read-only sentinel, simulating a thread whose follow-up window closed
	// between questions. The third run works again because the controller
	// must forget the read-only thread and create a fresh one.
	runs := 0
	manager := &sideManagerRunner{
		side: runnerFunc(func(
			ctx context.Context,
			_ RunInput,
			sink DisplayEventSink,
		) error {
			runs++
			if runs == 2 {
				return interaction.ErrSideThreadReadOnly
			}
			return sink(ctx, DisplayEvent{Kind: DisplayEventAgentEnd})
		}),
	}
	ctx, cancel := context.WithCancel(t.Context())
	requests := make(chan runRequest)
	done := make(chan struct{})
	go serveSideRuns(ctx, manager, requests, done)

	first := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "first question", updates: first}
	if terminal := drainRunUpdate(t, first); !terminal.done || terminal.err != nil {
		t.Fatalf("first terminal update = %#v", terminal)
	}
	if manager.createCalls != 1 || manager.openCalls != 0 {
		t.Fatalf(
			"after first question: create=%d open=%d, want 1/0",
			manager.createCalls,
			manager.openCalls,
		)
	}

	// The open succeeds but the run itself reports read-only at start.
	second := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "second question", updates: second}
	if terminal := drainRunUpdate(t, second); !terminal.done ||
		!errors.Is(terminal.err, interaction.ErrSideThreadReadOnly) {
		t.Fatalf("second terminal update = %#v", terminal)
	}
	if manager.createCalls != 1 || manager.openCalls != 1 {
		t.Fatalf(
			"after read-only run: create=%d open=%d, want 1/1",
			manager.createCalls,
			manager.openCalls,
		)
	}

	// The controller forgot the read-only thread, so the next question
	// starts a fresh thread instead of retrying it.
	third := make(chan runUpdate, runUpdateBuffer)
	requests <- runRequest{prompt: "third question", updates: third}
	if terminal := drainRunUpdate(t, third); !terminal.done || terminal.err != nil {
		t.Fatalf("third terminal update = %#v", terminal)
	}
	if manager.createCalls != 2 || manager.openCalls != 1 {
		t.Fatalf(
			"after fresh thread: create=%d open=%d, want 2/1",
			manager.createCalls,
			manager.openCalls,
		)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("side controller did not stop")
	}
}

func TestSideRunCancellationMessage(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current.side.isVisible = true
	current.side.isRunning = true
	current.side.assistantEntry = 0
	current.side.entries = []sideThreadEntry{{question: "question"}}
	current.finishSideRun(context.Canceled)
	if got := current.side.entries[0].err; got != "Side answer cancelled" {
		t.Fatalf("side cancellation message = %q", got)
	}
}
