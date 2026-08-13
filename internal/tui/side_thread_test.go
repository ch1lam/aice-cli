package tui

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// fakeSideManager is a thread-safe SideThreadManager double. It keeps a
// small registry so SideThreads/Open/Close behave like the app registry, and
// records calls for assertions.
type fakeSideManager struct {
	mu          sync.Mutex
	nextID      uint64
	threads     map[uint64]*fakeSideThread
	newRunner   func(id uint64) Runner
	createErr   error
	openErr     error
	closeErr    error
	createCalls int
	openCalls   int
	openIDs     []uint64
	closeCalls  int
	closeIDs    []uint64
}

type fakeSideThread struct {
	info    interaction.SideThread
	running bool
}

func newFakeSideManager() *fakeSideManager {
	return &fakeSideManager{threads: map[uint64]*fakeSideThread{}}
}

func testSideTitle(prompt string) string {
	runes := []rune(prompt)
	if len(runes) > 24 {
		return string(runes[:24])
	}
	return prompt
}

// fakeCreate simulates one controller-side creation without a controller.
func (f *fakeSideManager) fakeCreate(prompt string) interaction.SideThread {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createLocked(prompt, false)
}

func (f *fakeSideManager) createLocked(
	prompt string,
	recordCall bool,
) interaction.SideThread {
	if recordCall {
		f.createCalls++
	}
	f.nextID++
	info := interaction.SideThread{
		ID:           f.nextID,
		Title:        testSideTitle(prompt),
		Status:       interaction.SideThreadWritable,
		LastActiveAt: time.Now(),
	}
	f.threads[info.ID] = &fakeSideThread{info: info}
	return info
}

func (f *fakeSideManager) SideThreads() []interaction.SideThread {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := make([]interaction.SideThread, 0, len(f.threads))
	for _, thread := range f.threads {
		list = append(list, thread.info)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].LastActiveAt.Equal(list[j].LastActiveAt) {
			return list[i].ID > list[j].ID
		}
		return list[i].LastActiveAt.After(list[j].LastActiveAt)
	})
	return list
}

func (f *fakeSideManager) CreateSideThread(
	prompt string,
) (interaction.SideThread, Runner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return interaction.SideThread{}, nil, f.createErr
	}
	info := f.createLocked(prompt, true)
	runnerFactory := f.newRunner
	if runnerFactory != nil {
		return info, runnerFactory(info.ID), nil
	}
	return info, runnerFunc(func(
		context.Context,
		RunInput,
		DisplayEventSink,
	) error {
		return nil
	}), nil
}

func (f *fakeSideManager) OpenSideThread(
	id uint64,
) (interaction.SideThread, Runner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls++
	f.openIDs = append(f.openIDs, id)
	if f.openErr != nil {
		return interaction.SideThread{}, nil, f.openErr
	}
	thread, ok := f.threads[id]
	if !ok {
		return interaction.SideThread{}, nil, interaction.ErrSideThreadNotFound
	}
	info := thread.info
	runnerFactory := f.newRunner
	if runnerFactory != nil {
		return info, runnerFactory(id), nil
	}
	return info, runnerFunc(func(
		context.Context,
		RunInput,
		DisplayEventSink,
	) error {
		return nil
	}), nil
}

func (f *fakeSideManager) CloseSideThread(id uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	f.closeIDs = append(f.closeIDs, id)
	if f.closeErr != nil {
		return f.closeErr
	}
	thread, ok := f.threads[id]
	if !ok {
		return interaction.ErrSideThreadNotFound
	}
	if thread.running {
		return interaction.ErrSideThreadRunning
	}
	delete(f.threads, id)
	return nil
}

// seedThread inserts a thread without recording a create call.
func (f *fakeSideManager) seedThread(info interaction.SideThread) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threads[info.ID] = &fakeSideThread{info: info}
	if info.ID >= f.nextID {
		f.nextID = info.ID + 1
	}
}

// removeThread drops a thread as if it expired in the registry.
func (f *fakeSideManager) removeThread(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.threads, id)
}

func (f *fakeSideManager) threadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.threads)
}

// sideTestModel builds a model wired to a fake manager with a sized window.
func sideTestModel(t *testing.T, manager interaction.SideThreadManager) model {
	t.Helper()
	current := newModel(
		make(chan runRequest, 1),
		make(chan struct{}),
		btwSlashCommand(),
	)
	current.sideRequests = make(chan runRequest, 8)
	current.sideControllerDone = make(chan struct{})
	current.side.manager = manager
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	return current
}

// submitSide presses enter with the given composer value and returns the
// model plus the message produced by the returned command (nil when no
// command was issued).
func submitSide(t *testing.T, current model, value string) (model, tea.Msg) {
	t.Helper()
	current.input.SetValue(value)
	updated, command, handled := current.handleKey(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})
	if !handled {
		t.Fatal("enter was not handled")
	}
	if command == nil {
		return updated, nil
	}
	return updated, command()
}

// createSideThread runs one full new-thread round trip: submit, started
// message, then a batch carrying registry metadata plus the given updates.
func createSideThread(
	t *testing.T,
	current model,
	question string,
	updates ...runUpdate,
) (model, *fakeSideManager, interaction.SideThread, <-chan runUpdate) {
	t.Helper()
	manager := current.side.manager.(*fakeSideManager)
	requests := make(chan runRequest, 1)
	current.sideRequests = requests
	updated, message := submitSide(t, current, "/btw "+question)
	started, ok := message.(sideRunStartedMsg)
	if !ok || !started.isNew {
		t.Fatalf("start message = %#v, want new sideRunStartedMsg", message)
	}
	updated = updateModel(t, updated, started)
	info := manager.fakeCreate(question)
	batch := []runUpdate{{
		active:     &activeRunFunc{},
		cancel:     func() {},
		sideThread: &info,
	}}
	batch = append(batch, updates...)
	updated = updateModel(t, updated, sideRunBatchMsg{
		source:  started.updates,
		updates: batch,
	})
	select {
	case <-requests:
	default:
	}
	return updated, manager, info, started.updates
}

func TestModelBTWQuestionCreatesDistinctThreads(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)

	updated, _, first, _ := createSideThread(
		t,
		current,
		"question one",
		runUpdate{done: true},
	)
	thread := updated.side.thread(first.ID)
	if thread == nil || !thread.entries[0].complete {
		t.Fatalf("first thread state = %#v", thread)
	}
	if got := thread.entries[0].question; got != "question one" {
		t.Fatalf("first question = %q", got)
	}

	updated, _, second, _ := createSideThread(
		t,
		updated,
		"question two",
		runUpdate{done: true},
	)
	if first.ID == second.ID {
		t.Fatalf("threads share id %d", first.ID)
	}
	if got := updated.side.thread(second.ID).entries[0].question; got != "question two" {
		t.Fatalf("second question = %q", got)
	}
	if got := updated.side.thread(first.ID).entries[0].question; got != "question one" {
		t.Fatalf("first thread was disturbed: %q", got)
	}
	if updated.side.activeID != second.ID {
		t.Fatalf("active thread = %d, want %d", updated.side.activeID, second.ID)
	}
}

func TestModelBareBTWWithoutThreadsOpensNewComposerWithoutCreate(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)

	updated, message := submitSide(t, current, "/btw")
	if message != nil {
		t.Fatalf("bare /btw started a run: %#v", message)
	}
	if !updated.side.isVisible || updated.side.activeID != 0 {
		t.Fatalf("side state = %#v, want visible new composer", updated.side)
	}
	if manager.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", manager.createCalls)
	}
	if !updated.input.Focused() {
		t.Fatal("new composer is not focused")
	}
	if updated.input.Placeholder != sidePlaceholder {
		t.Fatalf("placeholder = %q, want %q", updated.input.Placeholder, sidePlaceholder)
	}
}

func TestModelBareBTWWithThreadsOpensMenu(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.seedThread(interaction.SideThread{
		ID:           1,
		Title:        "older thread",
		Status:       interaction.SideThreadWritable,
		LastActiveAt: time.Now().Add(-8 * time.Minute),
	})
	manager.seedThread(interaction.SideThread{
		ID:           2,
		Title:        "newer thread",
		Status:       interaction.SideThreadReadOnly,
		LastActiveAt: time.Now().Add(-30 * time.Minute),
	})
	current := sideTestModel(t, manager)

	updated, message := submitSide(t, current, "/btw")
	if message != nil {
		t.Fatalf("bare /btw started a run: %#v", message)
	}
	menu := updated.side.menu
	if menu == nil {
		t.Fatal("thread menu did not open")
	}
	if len(menu.options) != 2 ||
		menu.options[0].ID != 1 ||
		menu.options[1].ID != 2 {
		t.Fatalf("menu options = %#v, want newest first", menu.options)
	}

	view := ansi.Strip(updated.sideMenuView(80))
	for _, want := range []string{
		"New BTW thread",
		"#2",
		"#1",
		"Read-only · expires in 89m",
		"Follow-up · 8m idle",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("side menu = %q, want %q", view, want)
		}
	}

	// Selecting the New entry opens the blank new composer without creating.
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || updated.side.menu != nil || updated.side.activeID != 0 {
		t.Fatalf("after selecting New: menu=%v active=%d", updated.side.menu != nil, updated.side.activeID)
	}
	if manager.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", manager.createCalls)
	}
}

func TestSideMenuCancelRestoresDraftAndFocus(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.seedThread(interaction.SideThread{
		ID:           1,
		Title:        "existing",
		Status:       interaction.SideThreadWritable,
		LastActiveAt: time.Now(),
	})
	current := sideTestModel(t, manager)

	updated, message := submitSide(t, current, "/btw")
	if message != nil {
		t.Fatalf("bare /btw started a run: %#v", message)
	}
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || updated.side.menu != nil {
		t.Fatal("escape did not close the menu")
	}
	if updated.side.isVisible {
		t.Fatal("menu cancel left the panel visible")
	}
	if got := updated.input.Value(); got != "/btw" {
		t.Fatalf("draft after menu cancel = %q, want /btw", got)
	}
	if !updated.input.Focused() {
		t.Fatal("main composer is not focused after menu cancel")
	}
}

func TestSideMenuWorksWhileMainRunActive(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	info := manager.fakeCreate("background thread")
	current := sideTestModel(t, manager)
	current.running = true
	current.acceptsDelivery = true
	mainDeliveries := 0
	current.activeRun = &activeRunFunc{deliver: func(interaction.Delivery) error {
		mainDeliveries++
		return errors.New("menu reached the main mailbox")
	}}

	updated, _ := submitSide(t, current, "/btw")
	if updated.side.menu == nil {
		t.Fatal("menu did not open while the main run was active")
	}
	if len(updated.entries) != 0 {
		t.Fatalf("menu wrote main entries: %#v", updated.entries)
	}
	if mainDeliveries != 0 {
		t.Fatalf("menu delivered to the main run %d times", mainDeliveries)
	}

	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not move the menu selection")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || updated.side.menu != nil {
		t.Fatal("enter did not select the menu option")
	}
	if !updated.side.isVisible || updated.side.activeID != info.ID {
		t.Fatalf("panel state = %#v, want thread %d", updated.side, info.ID)
	}
	if !updated.running {
		t.Fatal("opening the menu stopped the main run")
	}
}

func TestModelFollowUpRoutesThroughOpenSideThread(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, _ := createSideThread(
		t,
		current,
		"first question",
		runUpdate{done: true},
	)

	requests := make(chan runRequest, 1)
	updated.sideRequests = requests
	updated, message := submitSide(t, updated, "second question")
	started, ok := message.(sideRunStartedMsg)
	if !ok || started.isNew {
		t.Fatalf("follow-up message = %#v, want existing-thread run", message)
	}
	request := <-requests
	if request.sideCreate || request.sideThreadID != info.ID {
		t.Fatalf("follow-up request = %#v, want thread %d", request, info.ID)
	}
	if request.prompt != "second question" {
		t.Fatalf("follow-up prompt = %q", request.prompt)
	}
}

func TestModelSwitchIsolatesEntriesAndDrafts(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, first, _ := createSideThread(
		t,
		current,
		"first question",
		runUpdate{done: true},
	)
	// Type a draft in thread 1 and hide the panel.
	updated.input.SetValue("draft for first")
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || updated.side.isVisible {
		t.Fatal("escape did not hide the panel")
	}

	updated, _, second, _ := createSideThread(
		t,
		updated,
		"second question",
		runUpdate{done: true},
	)
	if updated.side.thread(first.ID) == nil || updated.side.thread(second.ID) == nil {
		t.Fatal("both threads should exist")
	}

	// Reopen the first thread through the menu: its draft must come back.
	// Menu order is [New, #2, #1], so two downs reach the older thread.
	updated, _ = submitSide(t, updated, "/btw")
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not move the menu selection")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not move the menu selection")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || updated.side.activeID != first.ID {
		t.Fatalf("menu selection opened thread %d, want %d", updated.side.activeID, first.ID)
	}
	if got := updated.input.Value(); got != "draft for first" {
		t.Fatalf("thread 1 draft = %q, want restored draft", got)
	}
	view := ansi.Strip(updated.transcriptView())
	if !strings.Contains(view, "first question") || strings.Contains(view, "second question") {
		t.Fatalf("thread 1 transcript = %q", view)
	}

	// Switch to the second thread: separate entries, empty draft.
	updated, _ = submitSide(t, updated, "/btw")
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not reach the second thread")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || updated.side.activeID != second.ID {
		t.Fatalf("menu selection opened thread %d, want %d", updated.side.activeID, second.ID)
	}
	if got := updated.input.Value(); got != "" {
		t.Fatalf("thread 2 draft = %q, want empty", got)
	}
	view = ansi.Strip(updated.transcriptView())
	if !strings.Contains(view, "second question") || strings.Contains(view, "first question") {
		t.Fatalf("thread 2 transcript = %q", view)
	}
}

func TestModelReadOnlyThreadBlocksFollowUpButAllowsNew(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.seedThread(interaction.SideThread{
		ID:           1,
		Title:        "locked thread",
		Status:       interaction.SideThreadReadOnly,
		LastActiveAt: time.Now().Add(-30 * time.Minute),
	})
	current := sideTestModel(t, manager)
	requests := make(chan runRequest, 1)
	current.sideRequests = requests

	updated, _ := submitSide(t, current, "/btw")
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not move the menu selection")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || updated.side.activeID != 1 {
		t.Fatalf("panel did not open the read-only thread: active=%d", updated.side.activeID)
	}
	if updated.input.Focused() {
		t.Fatal("read-only composer must not be focused")
	}
	if !strings.Contains(updated.side.notice, "Read-only") {
		t.Fatalf("read-only notice = %q", updated.side.notice)
	}

	// A follow-up attempt is refused with a hint and no request.
	updated, message := submitSide(t, updated, "follow up")
	if message != nil {
		t.Fatalf("read-only follow-up started a run: %#v", message)
	}
	if !strings.Contains(updated.side.notice, "read-only") {
		t.Fatalf("read-only block notice = %q", updated.side.notice)
	}
	select {
	case request := <-requests:
		t.Fatalf("read-only follow-up emitted request %#v", request)
	default:
	}

	// /btw with a question still creates a brand-new thread.
	updated, message = submitSide(t, updated, "/btw fresh question")
	started, ok := message.(sideRunStartedMsg)
	if !ok || !started.isNew {
		t.Fatalf("new thread message = %#v", message)
	}
	request := <-requests
	if !request.sideCreate || request.prompt != "fresh question" {
		t.Fatalf("new thread request = %#v", request)
	}
	if manager.threadCount() != 1 {
		t.Fatal("thread was created before the controller handled the request")
	}
}

func TestModelExpiredVisibleThreadPrunedOnMenuOpen(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, _ := createSideThread(
		t,
		current,
		"question",
		runUpdate{done: true},
	)
	manager.removeThread(info.ID)

	updated, message := submitSide(t, updated, "/btw")
	if message != nil {
		t.Fatalf("bare /btw started a run: %#v", message)
	}
	if updated.side.menu != nil {
		t.Fatal("menu opened for an expired registry")
	}
	if updated.side.isVisible == false || updated.side.activeID != 0 {
		t.Fatalf("panel state = %#v, want new composer", updated.side)
	}
	if len(updated.side.threads) != 0 {
		t.Fatalf("expired thread not pruned: %#v", updated.side.threads)
	}
	if !strings.Contains(updated.side.notice, "expired") {
		t.Fatalf("expiry notice = %q", updated.side.notice)
	}
}

func TestModelExpiredFollowUpMovesQuestionToNewDraft(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, _ := createSideThread(
		t,
		current,
		"original question",
		runUpdate{done: true},
	)

	requests := make(chan runRequest, 1)
	updated.sideRequests = requests
	updated, message := submitSide(t, updated, "unsent follow up")
	started, ok := message.(sideRunStartedMsg)
	if !ok || started.isNew {
		t.Fatalf("follow-up message = %#v", message)
	}
	updated = updateModel(t, updated, started)
	manager.removeThread(info.ID)
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: started.updates,
		updates: []runUpdate{{
			err:  interaction.ErrSideThreadNotFound,
			done: true,
		}},
	})
	if updated.side.thread(info.ID) != nil || updated.side.isVisible {
		t.Fatalf("expired thread state = %#v", updated.side)
	}
	if got := updated.side.newDraft; got != "unsent follow up" {
		t.Fatalf("new draft = %q, want unsent follow up", got)
	}
	if got := updated.input.Value(); got != "unsent follow up" {
		t.Fatalf("main composer = %q, want unsent follow up", got)
	}
}

func TestModelCreateLimitKeepsDraft(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.createErr = interaction.ErrSideThreadLimit
	current := sideTestModel(t, manager)

	updated, message := submitSide(t, current, "/btw question")
	started, ok := message.(sideRunStartedMsg)
	if !ok || !started.isNew {
		t.Fatalf("start message = %#v", message)
	}
	updated = updateModel(t, updated, started)
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: started.updates,
		updates: []runUpdate{{
			err:  interaction.ErrSideThreadLimit,
			done: true,
		}},
	})

	if updated.side.newPending != nil {
		t.Fatal("pending creation survived the limit rejection")
	}
	if got := updated.side.newDraft; got != "question" {
		t.Fatalf("new draft = %q, want question", got)
	}
	if got := updated.input.Value(); got != "question" {
		t.Fatalf("composer after rejection = %q, want question", got)
	}
	if !strings.Contains(updated.side.notice, "limit") {
		t.Fatalf("limit notice = %q", updated.side.notice)
	}
	if len(updated.side.threads) != 0 {
		t.Fatalf("rejected create left a thread: %#v", updated.side.threads)
	}
}

func TestModelExistingThreadRejectionKeepsDraft(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, _ := createSideThread(
		t,
		current,
		"original question",
		runUpdate{done: true},
	)
	requests := make(chan runRequest, 1)
	updated.sideRequests = requests
	updated, message := submitSide(t, updated, "retry this follow up")
	started, ok := message.(sideRunStartedMsg)
	if !ok || started.isNew {
		t.Fatalf("follow-up message = %#v", message)
	}
	updated = updateModel(t, updated, started)
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: started.updates,
		updates: []runUpdate{{
			err:  interaction.ErrSideThreadReadOnly,
			done: true,
		}},
	})
	thread := updated.side.thread(info.ID)
	if thread == nil || thread.isRunning {
		t.Fatalf("thread after rejection = %#v", thread)
	}
	if thread.draft != "retry this follow up" ||
		updated.input.Value() != "retry this follow up" {
		t.Fatalf(
			"rejected draft = %q/%q",
			thread.draft,
			updated.input.Value(),
		)
	}
}

func TestModelConcurrencyRejectionRollsBackCreatedThread(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)

	updated, message := submitSide(t, current, "/btw question")
	started, ok := message.(sideRunStartedMsg)
	if !ok || !started.isNew {
		t.Fatalf("start message = %#v", message)
	}
	updated = updateModel(t, updated, started)
	info := manager.fakeCreate("question")
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: started.updates,
		updates: []runUpdate{
			{
				active:     &activeRunFunc{},
				cancel:     func() {},
				sideThread: &info,
			},
			{
				err:  interaction.ErrSideThreadConcurrencyLimit,
				done: true,
			},
		},
	})

	if len(updated.side.threads) != 0 {
		t.Fatalf("rolled-back thread still present: %#v", updated.side.threads)
	}
	if updated.side.newDraft != "question" {
		t.Fatalf("draft after rollback = %q", updated.side.newDraft)
	}
	if updated.side.activeID != 0 || updated.input.Value() != "question" {
		t.Fatalf(
			"rollback composer: active=%d input=%q",
			updated.side.activeID,
			updated.input.Value(),
		)
	}
	if len(manager.closeIDs) != 1 || manager.closeIDs[0] != info.ID {
		t.Fatalf("closed ids = %#v, want [%d]", manager.closeIDs, info.ID)
	}
}

func TestModelHiddenCompletionSetsUnreadAndReopenClears(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, ch := createSideThread(t, current, "question")

	// Hide the panel while the answer is still running.
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || updated.side.isVisible {
		t.Fatal("escape did not hide the panel")
	}
	updated = updateModel(t, updated, sideRunBatchMsg{
		source:  ch,
		updates: []runUpdate{{done: true}},
	})
	thread := updated.side.thread(info.ID)
	if thread == nil || !thread.hasUnread {
		t.Fatalf("hidden completion did not mark unread: %#v", thread)
	}
	header := ansi.Strip(updated.headerView(80))
	if !strings.Contains(header, "BTW 1 new") {
		t.Fatalf("header = %q, want unread indicator", header)
	}
	if len(updated.entries) != 0 {
		t.Fatalf("side completion leaked into main entries: %#v", updated.entries)
	}

	// Reopen through the menu clears the unread state.
	updated, _ = submitSide(t, updated, "/btw")
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("down did not move the menu selection")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || updated.side.activeID != info.ID {
		t.Fatalf("menu did not open thread %d", info.ID)
	}
	if updated.side.thread(info.ID).hasUnread {
		t.Fatal("reopening did not clear unread")
	}
}

func TestModelEscAndCtrlCOnlyAffectVisibleThread(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, message := submitSide(t, current, "/btw first")
	startedA, ok := message.(sideRunStartedMsg)
	if !ok || !startedA.isNew {
		t.Fatalf("start message = %#v", message)
	}
	updated = updateModel(t, updated, startedA)
	infoA := manager.fakeCreate("first")
	cancelledA := false
	cancelledB := false
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: startedA.updates,
		updates: []runUpdate{{
			active:     &activeRunFunc{},
			cancel:     func() { cancelledA = true },
			sideThread: &infoA,
		}},
	})

	// Hide the first thread and start a second, visible one.
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled {
		t.Fatal("escape was not handled")
	}
	updated, message = submitSide(t, updated, "/btw second")
	startedB, ok := message.(sideRunStartedMsg)
	if !ok || !startedB.isNew {
		t.Fatalf("start message = %#v", message)
	}
	updated = updateModel(t, updated, startedB)
	infoB := manager.fakeCreate("second")
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: startedB.updates,
		updates: []runUpdate{{
			active:     &activeRunFunc{},
			cancel:     func() { cancelledB = true },
			sideThread: &infoB,
		}},
	})
	if updated.side.activeID != infoB.ID {
		t.Fatalf("visible thread = %d, want %d", updated.side.activeID, infoB.ID)
	}

	// Ctrl+C cancels only the visible thread.
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{
		Code: 'c',
		Mod:  tea.ModCtrl,
	})
	if !handled {
		t.Fatal("ctrl+c was not handled")
	}
	if !cancelledB || cancelledA {
		t.Fatalf("cancellation: visible=%v hidden=%v", cancelledB, cancelledA)
	}
	if !updated.side.thread(infoA.ID).isRunning ||
		!updated.side.thread(infoB.ID).isRunning {
		t.Fatal("ctrl+c stopped a running thread")
	}

	// Escape hides without adding another cancellation.
	cancelledA = false
	cancelledB = false
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || updated.side.isVisible {
		t.Fatal("escape did not hide the panel")
	}
	if cancelledA || cancelledB {
		t.Fatal("escape cancelled a running thread")
	}
}

func TestModelCtrlDEndsIdleThread(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, _ := createSideThread(
		t,
		current,
		"question",
		runUpdate{done: true},
	)

	updated, _, handled := updated.handleKey(tea.KeyPressMsg{
		Code: 'd',
		Mod:  tea.ModCtrl,
	})
	if !handled {
		t.Fatal("ctrl+d was not handled")
	}
	if updated.side.isVisible {
		t.Fatal("panel stayed visible after ending the thread")
	}
	if updated.side.thread(info.ID) != nil {
		t.Fatal("ended thread still has local state")
	}
	if len(manager.closeIDs) != 1 || manager.closeIDs[0] != info.ID {
		t.Fatalf("closed ids = %#v, want [%d]", manager.closeIDs, info.ID)
	}
	if updated.status != "BTW thread ended" {
		t.Fatalf("status = %q, want end notice", updated.status)
	}
}

func TestModelCtrlDRunningThreadRequiresConfirmation(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, message := submitSide(t, current, "/btw question")
	started, ok := message.(sideRunStartedMsg)
	if !ok || !started.isNew {
		t.Fatalf("start message = %#v", message)
	}
	updated = updateModel(t, updated, started)
	info := manager.fakeCreate("question")
	cancelled := false
	updated = updateModel(t, updated, sideRunBatchMsg{
		source: started.updates,
		updates: []runUpdate{{
			active:     &activeRunFunc{},
			cancel:     func() { cancelled = true },
			sideThread: &info,
		}},
	})

	// n keeps the thread and its run.
	updated, _, handled := updated.handleKey(tea.KeyPressMsg{
		Code: 'd',
		Mod:  tea.ModCtrl,
	})
	if !handled || updated.side.confirm == nil {
		t.Fatal("ctrl+d did not open confirmation")
	}
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: 'n'})
	if !handled || updated.side.confirm != nil {
		t.Fatal("n did not cancel the confirmation")
	}
	if cancelled || updated.side.thread(info.ID) == nil {
		t.Fatal("n cancelled or deleted the thread")
	}

	// y cancels the run and waits for its termination before deleting.
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{
		Code: 'd',
		Mod:  tea.ModCtrl,
	})
	updated, _, handled = updated.handleKey(tea.KeyPressMsg{Code: 'y'})
	if !handled || updated.side.confirm != nil {
		t.Fatal("y did not confirm")
	}
	if !cancelled {
		t.Fatal("confirmation did not cancel the run")
	}
	if updated.side.thread(info.ID) == nil {
		t.Fatal("thread deleted before its run terminated")
	}
	updated = updateModel(t, updated, sideRunBatchMsg{
		source:  started.updates,
		updates: []runUpdate{{err: context.Canceled, done: true}},
	})
	if updated.side.thread(info.ID) != nil {
		t.Fatal("thread not deleted after its run terminated")
	}
	if len(manager.closeIDs) != 1 || manager.closeIDs[0] != info.ID {
		t.Fatalf("closed ids = %#v, want [%d]", manager.closeIDs, info.ID)
	}
	if updated.side.isVisible {
		t.Fatal("panel stayed visible after ending the thread")
	}
}

func TestSideRunUnavailableRestoresDraft(t *testing.T) {
	t.Parallel()

	t.Run("new thread", func(t *testing.T) {
		t.Parallel()

		manager := newFakeSideManager()
		current := sideTestModel(t, manager)
		controllerDone := make(chan struct{})
		current.sideControllerDone = controllerDone
		close(controllerDone)
		updated, message := submitSide(t, current, "/btw question")
		unavailable, ok := message.(sideRunUnavailableMsg)
		if !ok {
			t.Fatalf("message = %#v, want sideRunUnavailableMsg", message)
		}
		updated = updateModel(t, updated, unavailable)
		if !updated.side.closed {
			t.Fatal("controller not marked closed")
		}
		if updated.side.newDraft != "question" || updated.input.Value() != "question" {
			t.Fatalf("draft = %q/%q, want question", updated.side.newDraft, updated.input.Value())
		}
	})

	t.Run("follow-up", func(t *testing.T) {
		t.Parallel()

		manager := newFakeSideManager()
		current := sideTestModel(t, manager)
		updated, _, info, _ := createSideThread(
			t,
			current,
			"question",
			runUpdate{done: true},
		)
		controllerDone := make(chan struct{})
		updated.sideControllerDone = controllerDone
		updated.sideRequests = make(chan runRequest)
		close(controllerDone)
		updated, message := submitSide(t, updated, "follow up")
		unavailable, ok := message.(sideRunUnavailableMsg)
		if !ok {
			t.Fatalf("message = %#v, want sideRunUnavailableMsg", message)
		}
		updated = updateModel(t, updated, unavailable)
		thread := updated.side.thread(info.ID)
		if thread.isRunning || thread.updates != nil {
			t.Fatalf("thread after unavailable = %#v", thread)
		}
		if thread.draft != "follow up" {
			t.Fatalf("thread draft = %q, want follow up", thread.draft)
		}
	})
}

func TestModelStaleSideBatchIgnored(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	updated, _, info, ch := createSideThread(
		t,
		current,
		"question",
		runUpdate{done: true},
	)
	thread := updated.side.thread(info.ID)
	if thread.isRunning {
		t.Fatal("first run did not finish")
	}

	// A stale closed batch from the finished run must not disturb the
	// thread or start a new run chain.
	modelValue, command := updated.applySideRunBatch(sideRunBatchMsg{
		source: ch,
		closed: true,
	})
	updated = modelValue.(model)
	if command != nil {
		t.Fatal("stale batch returned a command")
	}
	if updated.side.thread(info.ID) == nil || updated.side.thread(info.ID).isRunning {
		t.Fatal("stale batch changed the thread state")
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

func TestSideThreadViewFitsNarrowTerminal(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	current := sideTestModel(t, manager)
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  minimumWidth,
		Height: 12,
	})
	info := manager.fakeCreate("narrow question")
	current.side.threads[info.ID] = &sideThreadState{
		id:    info.ID,
		title: info.Title,
		entries: []sideThreadEntry{{
			question: "a long side question that wraps on a narrow terminal",
			answer:   "a concise answer that also wraps",
			complete: true,
		}},
		assistantEntry: 0,
	}
	current.side.isVisible = true
	current.side.activeID = info.ID
	current.resizeLayout()
	if got := current.viewport.Width(); got != minimumWidth {
		t.Fatalf("side viewport width = %d, want %d", got, minimumWidth)
	}
	if view := current.View().Content; strings.TrimSpace(view) == "" {
		t.Fatal("narrow side view is empty")
	}
	// At the minimum width, the footer may omit help rather than overflow.
	_ = current.sideStatusLine(minimumWidth)
}

func TestSideMenuFitsNarrowTerminal(t *testing.T) {
	t.Parallel()

	manager := newFakeSideManager()
	manager.seedThread(interaction.SideThread{
		ID:           1,
		Title:        "a very long thread title that must be truncated on narrow terminals",
		Status:       interaction.SideThreadRunning,
		LastActiveAt: time.Now(),
	})
	current := sideTestModel(t, manager)
	current = updateModel(t, current, tea.WindowSizeMsg{
		Width:  minimumWidth,
		Height: 12,
	})
	updated, message := submitSide(t, current, "/btw")
	if message != nil {
		t.Fatalf("bare /btw started a run: %#v", message)
	}
	if updated.side.menu == nil {
		t.Fatal("menu did not open")
	}
	view := updated.sideMenuView(minimumWidth)
	if strings.TrimSpace(view) == "" {
		t.Fatal("narrow side menu is empty")
	}
	if view := updated.View().Content; strings.TrimSpace(view) == "" {
		t.Fatal("narrow menu view is empty")
	}
}
