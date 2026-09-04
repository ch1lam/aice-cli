package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

const (
	maximumSideEntries = 20
	sidePlaceholder    = "Ask a side question about this Session..."
	sideNewTitle       = "New BTW thread"
)

func btwSlashCommand() SlashCommand {
	return SlashCommand{
		Name:         "btw",
		Description:  "Ask an ephemeral side question without interrupting the main task",
		ArgumentHint: "[question]",
	}
}

type sideThreadEntry struct {
	question string
	answer   string
	thinking string
	err      string
	complete bool
}

// sideThreadState is the presentation state for one registry thread. The
// registry in internal/app owns lifecycle truth; this mirrors enough of it
// to render the panel and route events.
type sideThreadState struct {
	id              uint64
	title           string
	status          interaction.SideThreadStatus
	lastActiveAt    time.Time
	updates         <-chan runUpdate
	cancel          context.CancelFunc
	entries         []sideThreadEntry
	assistantEntry  int
	draft           string
	isRunning       bool
	cancelPending   bool
	closing         bool
	hasUnread       bool
	receivedContent bool
}

// pendingSideRun tracks a brand-new thread whose first question is in flight:
// the thread only exists in the registry once its first run starts.
type pendingSideRun struct {
	question    string
	ch          <-chan runUpdate
	fromVisible bool
}

// sideMenuState is the local /btw thread chooser. Options are a defensive
// manager snapshot plus the synthetic New entry (ID 0).
type sideMenuState struct {
	fromPanel   bool
	returnDraft string
	options     []interaction.SideThread
	selection   int
}

// sideConfirmState is the confirmation prompt for ending a running thread.
type sideConfirmState struct {
	threadID uint64
}

// sidePanelState owns every ephemeral BTW thread shown by the TUI. The main
// run keeps its own transcript and controllers; this state only interacts
// with the side-thread registry.
type sidePanelState struct {
	manager    interaction.SideThreadManager
	isVisible  bool
	activeID   uint64
	newDraft   string
	newPending *pendingSideRun
	threads    map[uint64]*sideThreadState
	menu       *sideMenuState
	confirm    *sideConfirmState
	notice     string
	closed     bool
}

func (s *sidePanelState) thread(id uint64) *sideThreadState {
	if s == nil {
		return nil
	}
	return s.threads[id]
}

func (s *sidePanelState) activeThread() *sideThreadState {
	return s.thread(s.activeID)
}

func (s *sidePanelState) anyRunning() bool {
	if s.newPending != nil {
		return true
	}
	for _, thread := range s.threads {
		if thread.isRunning {
			return true
		}
	}
	return false
}

func (s *sidePanelState) unreadCount() int {
	count := 0
	for _, thread := range s.threads {
		if thread.hasUnread {
			count++
		}
	}
	return count
}

func (t *sideThreadState) activeEntry() *sideThreadEntry {
	if t == nil || t.assistantEntry < 0 ||
		t.assistantEntry >= len(t.entries) {
		return nil
	}
	return &t.entries[t.assistantEntry]
}

func (t *sideThreadState) readOnly() bool {
	return t != nil && t.status == interaction.SideThreadReadOnly
}

// sideFriendlyError translates expected registry failures into user-facing
// messages; unexpected errors pass through as-is.
func sideFriendlyError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "Side answer cancelled"
	case errors.Is(err, interaction.ErrSideThreadReadOnly):
		return "This thread is read-only; /btw starts a new thread"
	case errors.Is(err, interaction.ErrSideThreadLimit):
		return "BTW thread limit reached; end a thread first"
	case errors.Is(err, interaction.ErrSideThreadConcurrencyLimit):
		return "Too many side answers running; try again shortly"
	case errors.Is(err, interaction.ErrSideThreadNotFound):
		return "BTW thread expired and was deleted"
	case errors.Is(err, interaction.ErrSideThreadBusy):
		return "This thread is already answering"
	case errors.Is(err, interaction.ErrSideThreadRunning):
		return "This thread is still answering"
	}
	return err.Error()
}

// reconcileSideThreads aligns local thread state with a manager snapshot:
// threads the registry no longer knows are pruned, and statuses and
// timestamps are refreshed for the rest. It reports whether the currently
// visible thread was pruned.
func (m *model) reconcileSideThreads(snapshot []interaction.SideThread) bool {
	alive := make(map[uint64]interaction.SideThread, len(snapshot))
	for _, info := range snapshot {
		alive[info.ID] = info
	}
	prunedVisible := false
	for id, thread := range m.side.threads {
		info, ok := alive[id]
		if !ok {
			delete(m.side.threads, id)
			if m.side.isVisible && m.side.activeID == id {
				prunedVisible = true
			}
			continue
		}
		thread.status = info.Status
		thread.lastActiveAt = info.LastActiveAt
		thread.title = info.Title
	}
	return prunedVisible
}

// refreshSideThreads reconciles local state against the manager. It returns
// whether the currently visible thread was pruned.
func (m *model) refreshSideThreads() bool {
	if m.side.manager == nil {
		return false
	}
	return m.reconcileSideThreads(m.side.manager.SideThreads())
}

// sideThreadAlive reports whether the registry still knows one thread.
func (m *model) sideThreadAlive(id uint64) bool {
	if m.side.manager == nil {
		return false
	}
	for _, info := range m.side.manager.SideThreads() {
		if info.ID == id {
			return true
		}
	}
	return false
}

type sideRunStartedMsg struct {
	updates  <-chan runUpdate
	threadID uint64
	question string
	isNew    bool
}

type sideRunUnavailableMsg struct {
	threadID uint64
	question string
	isNew    bool
}

type sideRunBatchMsg struct {
	source  <-chan runUpdate
	updates []runUpdate
	closed  bool
}

func startSideRun(
	requests chan<- runRequest,
	controllerDone <-chan struct{},
	prompt string,
	create bool,
	threadID uint64,
) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan runUpdate, runUpdateBuffer)
		unavailable := sideRunUnavailableMsg{
			threadID: threadID,
			question: prompt,
			isNew:    create,
		}
		select {
		case <-controllerDone:
			return unavailable
		default:
		}
		select {
		case <-controllerDone:
			return unavailable
		case requests <- runRequest{
			prompt:       prompt,
			updates:      updates,
			sideCreate:   create,
			sideThreadID: threadID,
		}:
			return sideRunStartedMsg{
				updates:  updates,
				threadID: threadID,
				question: prompt,
				isNew:    create,
			}
		}
	}
}

func waitForSideRunUpdates(updates <-chan runUpdate) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-updates
		if !ok {
			return sideRunBatchMsg{source: updates, closed: true}
		}
		batch := sideRunBatchMsg{
			source:  updates,
			updates: []runUpdate{first},
		}
		for len(batch.updates) < maximumEventBatch {
			select {
			case update, open := <-updates:
				if !open {
					batch.closed = true
					return batch
				}
				batch.updates = append(batch.updates, update)
			default:
				return batch
			}
		}
		return batch
	}
}

// applySideRunBatch routes one update batch to the thread it belongs to by
// matching the source channel. Batches from finished or superseded runs are
// dropped.
func (m model) applySideRunBatch(batch sideRunBatchMsg) (tea.Model, tea.Cmd) {
	if pending := m.side.newPending; pending != nil && batch.source == pending.ch {
		return m.applyNewSideRunBatch(batch)
	}
	for _, thread := range m.side.threads {
		if thread.updates == batch.source {
			return m.applySideThreadBatch(batch, thread)
		}
	}
	return m, nil
}

// applyNewSideRunBatch handles the first run of a brand-new thread. The run's
// first update carries the registry metadata that creates the local thread
// state. If the run never produced content, the thread is rolled back so the
// question survives as a draft and no empty thread lingers in the registry.
func (m model) applyNewSideRunBatch(batch sideRunBatchMsg) (tea.Model, tea.Cmd) {
	pending := m.side.newPending
	if pending == nil || batch.source != pending.ch {
		return m, nil
	}
	var commands []tea.Cmd
	var thread *sideThreadState
	created := false
	answered := false
	terminal := false
	var terminalErr error

	for _, update := range batch.updates {
		if update.sideThread != nil {
			info := *update.sideThread
			thread = &sideThreadState{
				id:             info.ID,
				title:          info.Title,
				status:         info.Status,
				lastActiveAt:   info.LastActiveAt,
				updates:        batch.source,
				entries:        []sideThreadEntry{{question: pending.question}},
				assistantEntry: 0,
				isRunning:      true,
				cancel:         update.cancel,
			}
			// Only claim the panel when the user is still on the new composer:
			// an Esc or navigation to another thread keeps this thread
			// answering in the background.
			claimPanel := pending.fromVisible && m.side.isVisible &&
				m.side.activeID == 0 &&
				m.side.menu == nil && m.side.confirm == nil
			m.side.threads[info.ID] = thread
			m.side.newPending = nil
			if claimPanel {
				m.side.activeID = info.ID
				m.side.notice = "Starting side answer..."
			}
			created = true
			continue
		}
		if update.done {
			terminal = true
			terminalErr = update.err
			continue
		}
		if created && m.applySideEvent(thread, update.event) {
			answered = true
		}
	}

	restoreNewDraft := func() {
		m.side.newDraft = pending.question
		m.side.notice = sideFriendlyError(terminalErr)
		if m.side.notice == "" {
			m.side.notice = "The BTW side-thread controller stopped"
		}
		if pending.fromVisible && m.side.isVisible &&
			m.side.activeID == 0 &&
			m.side.menu == nil && m.side.confirm == nil {
			m.pastes = nil
			m.input.SetValue(m.side.newDraft)
			m.input.CursorEnd()
			m.input.Focus()
			m.resizeLayout()
			m.refreshViewport(true)
		}
	}

	if !created {
		// The registry rejected the creation: keep the question as the new
		// composer draft and explain why.
		m.side.newPending = nil
		restoreNewDraft()
		return m, nil
	}

	if terminal && terminalErr != nil && !answered {
		// The first interaction never produced content (for example the
		// global concurrency limit). Roll the thread back so the question
		// stays as a draft and the registry does not keep an empty thread.
		if !m.side.closed && m.side.manager != nil {
			_ = m.side.manager.CloseSideThread(thread.id)
		}
		delete(m.side.threads, thread.id)
		m.side.activeID = 0
		m.side.newPending = nil
		restoreNewDraft()
		return m, nil
	}

	if terminal {
		m.finishSideRun(thread, terminalErr)
	}
	if batch.closed {
		thread.updates = nil
		if thread.isRunning {
			m.finishSideRun(thread, errors.New(
				"side run ended without a terminal update",
			))
		}
	} else if thread.updates != nil {
		commands = append(commands, waitForSideRunUpdates(thread.updates))
	}
	m.resizeLayout()
	m.refreshViewport(true)
	return m, tea.Batch(commands...)
}

// applySideThreadBatch applies updates for one existing thread's run.
func (m model) applySideThreadBatch(
	batch sideRunBatchMsg,
	thread *sideThreadState,
) (tea.Model, tea.Cmd) {
	if batch.source != thread.updates {
		return m, nil
	}
	var commands []tea.Cmd
	follow := m.viewport.AtBottom()
	changed := false
	finished := false
	visible := m.side.isVisible && m.side.activeID == thread.id

	for _, update := range batch.updates {
		if update.cancel != nil {
			thread.cancel = update.cancel
			if thread.cancelPending {
				thread.cancel()
				thread.cancelPending = false
				if visible {
					m.side.notice = "Cancelling side answer..."
				}
			} else if visible {
				m.side.notice = "Thinking..."
			}
			changed = true
		}
		if update.done {
			closing := thread.closing
			m.finishSideRun(thread, update.err)
			if closing {
				return m.endSideThreadNow(thread.id)
			}
			finished = true
			changed = true
			continue
		}
		if m.applySideEvent(thread, update.event) {
			changed = true
		}
	}

	if batch.closed {
		thread.updates = nil
		if thread.isRunning {
			closing := thread.closing
			m.finishSideRun(thread, errors.New(
				"side run ended without a terminal update",
			))
			if closing {
				return m.endSideThreadNow(thread.id)
			}
			finished = true
			changed = true
		}
	} else if thread.updates != nil {
		commands = append(commands, waitForSideRunUpdates(thread.updates))
	}

	if changed {
		if visible {
			m.resizeLayout()
			m.refreshViewport(follow || finished)
		} else if !m.side.isVisible {
			// A hidden completion changes the header's unread indicator.
			m.refreshViewport(false)
		}
	}
	return m, tea.Batch(commands...)
}

func (m *model) applySideEvent(
	thread *sideThreadState,
	event DisplayEvent,
) bool {
	entry := thread.activeEntry()
	switch event.Kind {
	case DisplayEventAssistantStart:
		if entry == nil {
			return false
		}
		thread.receivedContent = true
		entry.answer = ""
		entry.thinking = ""
		entry.err = ""
		entry.complete = false
		if m.side.isVisible && m.side.activeID == thread.id {
			m.side.notice = "Thinking..."
		}
	case DisplayEventAssistantDelta:
		if entry == nil {
			return false
		}
		thread.receivedContent = true
		switch event.Delta.Kind {
		case DisplayDeltaText:
			entry.answer += event.Delta.Delta
			if m.side.isVisible && m.side.activeID == thread.id {
				m.side.notice = "Answering..."
			}
		case DisplayDeltaThinking:
			entry.thinking += event.Delta.Delta
			if m.side.isVisible && m.side.activeID == thread.id {
				m.side.notice = "Thinking..."
			}
		case DisplayDeltaToolCall:
			if m.side.isVisible && m.side.activeID == thread.id {
				m.side.notice = "Side questions cannot use tools"
			}
		default:
			return false
		}
	case DisplayEventAssistantEnd:
		if entry == nil {
			return false
		}
		thread.receivedContent = true
		entry.answer = event.Assistant.Text
		entry.thinking = event.Assistant.Thinking
		entry.complete = true
		if m.side.isVisible && m.side.activeID == thread.id {
			m.side.notice = "Answer complete"
		}
	case DisplayEventToolStart, DisplayEventToolEnd:
		if m.side.isVisible && m.side.activeID == thread.id {
			m.side.notice = "Side questions cannot use tools"
		}
	case DisplayEventRetryStart:
		if m.side.isVisible && m.side.activeID == thread.id {
			m.side.notice = "Retrying side answer..."
		}
	case DisplayEventRetryEnd:
		if m.side.isVisible && m.side.activeID == thread.id {
			if event.Retry.Succeeded {
				m.side.notice = "Retry succeeded"
			} else {
				m.side.notice = "Retry stopped"
			}
		}
	case DisplayEventAgentEnd:
		if event.Err == nil && m.side.isVisible && m.side.activeID == thread.id {
			m.side.notice = "Answer complete"
		}
	default:
		return false
	}
	return true
}

// finishSideRun settles one thread after its run terminates for any reason.
// The registry is refreshed as the lifecycle authority, so a thread that
// expired or was closed elsewhere is pruned instead of lingering.
func (m *model) finishSideRun(thread *sideThreadState, err error) {
	visible := m.side.isVisible && m.side.activeID == thread.id
	if err != nil {
		message := sideFriendlyError(err)
		if entry := thread.activeEntry(); entry != nil {
			entry.err = message
			entry.complete = true
		}
		// A run rejected before producing any content restores the question
		// as a draft instead of losing it.
		if !thread.receivedContent {
			if entry := thread.activeEntry(); entry != nil {
				thread.draft = entry.question
			}
		}
		if visible {
			m.side.notice = message
		}
	} else {
		if entry := thread.activeEntry(); entry != nil {
			entry.complete = true
		}
		if visible {
			m.side.notice = "Ready for another side question"
		}
	}
	thread.isRunning = false
	thread.cancel = nil
	thread.cancelPending = false
	thread.assistantEntry = -1
	thread.updates = nil

	if visible {
		m.pastes = nil
		m.input.SetValue(thread.draft)
		m.input.CursorEnd()
		if thread.readOnly() || thread.isRunning {
			m.input.Blur()
		} else {
			m.input.Focus()
		}
	} else {
		thread.hasUnread = true
	}

	if m.refreshSideThreads() && visible {
		if strings.TrimSpace(thread.draft) != "" {
			m.side.newDraft = thread.draft
		}
		m.status = "BTW thread expired and was deleted"
		m.closeSidePanelToMain()
		if m.side.newDraft != "" {
			m.pastes = nil
			m.input.SetValue(m.side.newDraft)
			m.input.CursorEnd()
		}
	}
}

// endSideThreadNow permanently deletes a thread in the registry and drops all
// local presentation state for it. If the deleted thread was visible, the
// panel returns to the main view.
func (m model) endSideThreadNow(id uint64) (model, tea.Cmd) {
	if !m.side.closed && m.side.manager != nil {
		_ = m.side.manager.CloseSideThread(id)
	}
	delete(m.side.threads, id)
	m.side.confirm = nil
	if m.side.isVisible && m.side.activeID == id {
		m.status = "BTW thread ended"
		m.closeSidePanelToMain()
	} else {
		m.resizeLayout()
	}
	return m, nil
}

// closeSidePanelToMain hides the side panel and returns focus to whichever
// composer is enabled.
func (m *model) closeSidePanelToMain() {
	m.side.isVisible = false
	m.side.activeID = 0
	m.side.menu = nil
	m.side.confirm = nil
	m.input.Reset()
	m.pastes = nil
	m.input.Placeholder = defaultPlaceholder
	if m.composerInputEnabled() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.resizeLayout()
	m.refreshViewport(true)
}

func (m model) trimSideEntries(entries []sideThreadEntry) []sideThreadEntry {
	if len(entries) <= maximumSideEntries {
		return entries
	}
	return append([]sideThreadEntry{}, entries[len(entries)-maximumSideEntries:]...)
}
