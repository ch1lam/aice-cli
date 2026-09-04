package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func (m model) isBTWCommandInput() bool {
	if len(m.pastes) > 0 {
		return false
	}
	request, slash := parseSlashCommand(m.input.Value())
	return slash && request.Name == "btw"
}

// handleBTWCommand routes one /btw invocation. A question always starts a
// brand-new thread; a bare /btw opens the thread menu when threads exist, or
// the blank new-thread composer otherwise. returnDraft is restored when a
// menu is cancelled; fromPanel marks invocations typed inside the side panel.
func (m model) handleBTWCommand(
	request SlashCommandRequest,
	returnDraft string,
	fromPanel bool,
) (model, tea.Cmd, bool) {
	if m.side.manager == nil || m.side.closed {
		if fromPanel {
			m.side.notice = "The BTW side-thread controller is unavailable"
			return m, nil, true
		}
		return m.commandError(
			returnDraft,
			"The BTW side-thread controller is unavailable",
		)
	}
	if strings.TrimSpace(request.Arguments) != "" {
		return m.startNewSideThread(strings.TrimSpace(request.Arguments))
	}
	return m.openSideMenu(fromPanel, returnDraft)
}

// openSideMenu shows the /btw thread chooser. With no live threads it opens
// the blank new-thread composer directly instead.
func (m model) openSideMenu(
	fromPanel bool,
	returnDraft string,
) (model, tea.Cmd, bool) {
	if m.side.manager == nil || m.side.closed {
		m.side.notice = "The BTW side-thread controller is unavailable"
		return m, nil, true
	}
	if m.side.newPending != nil {
		m.side.notice = "Another side question is still starting"
		return m, nil, true
	}
	snapshot := m.side.manager.SideThreads()
	if m.reconcileSideThreads(snapshot) {
		// The visible thread expired while the panel was open.
		m.side.notice = "BTW thread expired and was deleted"
		return m.openNewSideComposer(), nil, true
	}
	if len(snapshot) == 0 {
		return m.openNewSideComposer(), nil, true
	}
	m.side.menu = &sideMenuState{
		fromPanel:   fromPanel,
		returnDraft: returnDraft,
		options:     snapshot,
	}
	m.side.notice = "Choose a BTW thread"
	m.input.Blur()
	m.resizeLayout()
	m.refreshViewport(false)
	return m, nil, true
}

// openNewSideComposer shows the blank new-thread composer. No registry entry
// is created until the first question is submitted.
func (m model) openNewSideComposer() model {
	m.side.isVisible = true
	m.side.activeID = 0
	m.side.menu = nil
	m.side.confirm = nil
	m.input.Reset()
	m.pastes = nil
	m.input.Placeholder = sidePlaceholder
	m.input.SetValue(m.side.newDraft)
	m.input.CursorEnd()
	m.input.Focus()
	if strings.TrimSpace(m.side.notice) == "" {
		m.side.notice = "Ask a quick question using context AICE already has"
	}
	m.resizeLayout()
	m.refreshViewport(true)
	return m
}

// startNewSideThread submits the first question of a brand-new thread. The
// registry freezes the parent context and assigns the id only when the run
// actually starts.
func (m model) startNewSideThread(question string) (model, tea.Cmd, bool) {
	if m.side.manager == nil || m.side.closed {
		m.side.notice = "The BTW side-thread controller is unavailable"
		return m, nil, true
	}
	if m.side.newPending != nil {
		m.side.notice = "Another side question is still starting"
		return m, nil, true
	}
	// Save the draft of whatever composer is active before switching to the
	// new-thread composer. Drafts keep the expanded text.
	if m.side.isVisible {
		if thread := m.side.activeThread(); thread != nil {
			thread.draft = m.expandComposerText()
		} else if m.side.activeID == 0 {
			m.side.newDraft = m.expandComposerText()
		}
		m.pastes = nil
	}
	m.side.isVisible = true
	m.side.activeID = 0
	m.side.newDraft = ""
	m.side.newPending = &pendingSideRun{
		question:    question,
		fromVisible: true,
	}
	m.side.notice = "Starting side answer..."
	m.input.Reset()
	m.pastes = nil
	m.input.Placeholder = sidePlaceholder
	m.input.Blur()
	return m.settleCommand(
		true,
		startSideRun(m.sideRequests, m.sideControllerDone, question, true, 0),
	)
}

// submitSideComposer handles Enter inside the side panel: a new-thread
// question when the new composer is active, a follow-up for the visible
// thread otherwise. /btw inside either composer always creates a new thread.
func (m model) submitSideComposer() (model, tea.Cmd, bool) {
	if m.side.activeID == 0 {
		if m.side.newPending != nil {
			return m, nil, true
		}
		question := strings.TrimSpace(m.expandComposerText())
		if question == "" {
			m.side.notice = "A side question is required"
			return m, nil, true
		}
		if len(m.pastes) == 0 {
			if request, slash := parseSlashCommand(question); slash &&
				request.Name == "btw" {
				if strings.TrimSpace(request.Arguments) == "" {
					return m.openSideMenu(true, question)
				}
				return m.startNewSideThread(strings.TrimSpace(request.Arguments))
			}
		}
		return m.startNewSideThread(question)
	}

	thread := m.side.activeThread()
	if thread == nil || thread.isRunning {
		return m, nil, true
	}
	question := strings.TrimSpace(m.expandComposerText())
	if question == "" {
		return m, nil, true
	}
	if len(m.pastes) == 0 {
		if request, slash := parseSlashCommand(question); slash &&
			request.Name == "btw" {
			if strings.TrimSpace(request.Arguments) == "" {
				return m.openSideMenu(true, question)
			}
			return m.startNewSideThread(strings.TrimSpace(request.Arguments))
		}
	}
	if thread.readOnly() {
		m.side.notice = "This thread is read-only; /btw starts a new thread"
		return m, nil, true
	}
	return m.submitSideFollowUp(question)
}

// submitSideFollowUp queues one more question on the visible thread through
// the registry's OpenSideThread path.
func (m model) submitSideFollowUp(question string) (model, tea.Cmd, bool) {
	thread := m.side.activeThread()
	if thread == nil || thread.isRunning {
		return m, nil, true
	}
	thread.entries = append(thread.entries, sideThreadEntry{question: question})
	thread.entries = m.trimSideEntries(thread.entries)
	thread.assistantEntry = len(thread.entries) - 1
	thread.isRunning = true
	thread.receivedContent = false
	thread.cancelPending = false
	thread.draft = ""
	m.side.notice = "Starting side answer..."
	m.input.Reset()
	m.pastes = nil
	m.input.Placeholder = sidePlaceholder
	m.input.Blur()
	return m.settleCommand(
		true,
		startSideRun(
			m.sideRequests,
			m.sideControllerDone,
			question,
			false,
			thread.id,
		),
	)
}

// openSideThreadPanel switches the panel to an existing thread, refreshing
// its status from the registry and clearing unread state. Local presentation
// state is built lazily when the registry knows the thread but no run was
// ever observed.
func (m model) openSideThreadPanel(info interaction.SideThread) (model, tea.Cmd, bool) {
	thread := m.side.thread(info.ID)
	if thread == nil {
		if !m.sideThreadAlive(info.ID) {
			m.side.notice = "BTW thread expired and was deleted"
			return m.openNewSideComposer(), nil, true
		}
		thread = &sideThreadState{
			id:           info.ID,
			title:        info.Title,
			status:       info.Status,
			lastActiveAt: info.LastActiveAt,
		}
		m.side.threads[info.ID] = thread
	}
	thread.hasUnread = false
	m.side.isVisible = true
	m.side.activeID = info.ID
	m.side.confirm = nil
	m.input.Placeholder = sidePlaceholder
	m.input.SetValue(thread.draft)
	m.input.CursorEnd()
	if thread.isRunning || thread.readOnly() {
		m.input.Blur()
	} else {
		m.input.Focus()
	}
	switch {
	case thread.isRunning:
		m.side.notice = "Side answer in progress"
	case thread.readOnly():
		m.side.notice = sideReadOnlyNotice(thread)
	default:
		m.side.notice = "Side thread reopened"
	}
	m.resizeLayout()
	m.refreshViewport(true)
	return m, nil, true
}

// closeSideThread hides the panel without cancelling anything and without
// touching the registry.
func (m model) closeSideThread() (model, tea.Cmd, bool) {
	if m.side.activeID == 0 {
		m.side.newDraft = m.expandComposerText()
	} else if thread := m.side.activeThread(); thread != nil {
		thread.draft = m.expandComposerText()
	}
	m.side.isVisible = false
	m.side.activeID = 0
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
	return m, nil, true
}

// sideComposerEditable mirrors the newline guards below: the side composer
// accepts text only for a pending-free new thread or an idle, writable
// thread.
func (m model) sideComposerEditable() bool {
	if m.side.activeID == 0 {
		return m.side.newPending == nil
	}
	thread := m.side.activeThread()
	return thread != nil && !thread.isRunning && !thread.readOnly()
}

func (m model) handleSideKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if !m.side.isVisible {
		return m, nil, false
	}
	thread := m.side.activeThread()

	if m.sideComposerEditable() {
		// Ctrl+G edits the composer in the default editor; placeholder
		// tokens stay atomic for cursor motion and deletion.
		if key.Matches(message, m.keys.editor) {
			updated, command := m.openComposerEditor()
			return updated, command, true
		}
		if updated, command, handled := m.handlePasteTokenKey(message); handled {
			updated.resizeLayout()
			return updated, command, true
		}
	}

	switch {
	case message.Code == tea.KeyEscape:
		return m.closeSideThread()
	case key.Matches(message, m.keys.interrupt):
		if thread == nil || !thread.isRunning {
			m.side.notice = "No side answer is running"
			return m, nil, true
		}
		if thread.cancel != nil {
			thread.cancel()
		} else {
			thread.cancelPending = true
		}
		m.side.notice = "Cancelling side answer..."
		m.refreshViewport(false)
		return m, nil, true
	case key.Matches(message, m.keys.quit):
		return m.requestEndSideThread()
	case m.helpToggleRequested(message):
		m.help.ShowAll = !m.help.ShowAll
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	case key.Matches(message, m.keys.newline):
		if m.side.activeID == 0 {
			if m.side.newPending == nil {
				m.input.InsertString("\n")
				m.resizeLayout()
			}
		} else if thread != nil && !thread.isRunning && !thread.readOnly() {
			m.input.InsertString("\n")
			m.resizeLayout()
		}
		return m, nil, true
	case key.Matches(message, m.keys.send):
		return m.submitSideComposer()
	case key.Matches(message, m.keys.queue),
		key.Matches(message, m.keys.process):
		return m, nil, true
	case key.Matches(message, m.keys.scroll):
		switch message.Code {
		case tea.KeyPgUp:
			m.viewport.PageUp()
		case tea.KeyPgDown:
			m.viewport.PageDown()
		}
		return m, nil, true
	}
	return m, nil, false
}

// requestEndSideThread handles Ctrl+D inside the side panel: an idle thread
// is deleted immediately, a running one goes through the confirmation flow.
func (m model) requestEndSideThread() (model, tea.Cmd, bool) {
	thread := m.side.activeThread()
	if thread == nil {
		m.side.notice = "No BTW thread is open"
		return m, nil, true
	}
	if thread.isRunning {
		m.side.confirm = &sideConfirmState{threadID: thread.id}
		m.side.notice = "End this thread? Its answer will be cancelled. y confirm · n cancel"
		m.refreshViewport(false)
		return m, nil, true
	}
	updated, command := m.endSideThreadNow(thread.id)
	return updated, command, true
}

func (m model) handleSideMenuKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	switch {
	case message.Code == tea.KeyUp:
		m.moveSideMenuSelection(-1)
		m.refreshViewport(false)
		return m, nil, true
	case message.Code == tea.KeyDown:
		m.moveSideMenuSelection(1)
		m.refreshViewport(false)
		return m, nil, true
	case message.Code == tea.KeyTab,
		key.Matches(message, m.keys.send):
		return m.selectSideMenuOption()
	case message.Code == tea.KeyEscape:
		return m.cancelSideMenu()
	}
	return m, nil, true
}

func (m *model) moveSideMenuSelection(delta int) {
	menu := m.side.menu
	if menu == nil {
		return
	}
	optionCount := len(menu.options) + 1 // synthetic New entry
	menu.selection = (menu.selection + delta + optionCount) % optionCount
}

func (m model) selectSideMenuOption() (model, tea.Cmd, bool) {
	menu := m.side.menu
	if menu == nil {
		return m, nil, true
	}
	selection := min(max(menu.selection, 0), len(menu.options))
	m.side.menu = nil
	if selection == 0 {
		return m.openNewSideComposer(), nil, true
	}
	return m.openSideThreadPanel(menu.options[selection-1])
}

func (m model) cancelSideMenu() (model, tea.Cmd, bool) {
	menu := m.side.menu
	m.side.menu = nil
	if menu == nil {
		return m, nil, true
	}
	m.side.notice = "BTW selection cancelled"
	m.pastes = nil
	m.input.SetValue(menu.returnDraft)
	m.input.CursorEnd()
	if menu.fromPanel {
		if m.composerInputEnabled() {
			m.input.Focus()
		} else {
			m.input.Blur()
		}
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	}
	m.side.isVisible = false
	m.side.activeID = 0
	m.input.Reset()
	m.pastes = nil
	m.input.Placeholder = defaultPlaceholder
	m.input.SetValue(menu.returnDraft)
	m.input.CursorEnd()
	m.input.Focus()
	m.resizeLayout()
	m.refreshViewport(true)
	return m, nil, true
}

func (m model) handleSideConfirmKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	confirm := m.side.confirm
	if confirm == nil {
		return m, nil, true
	}
	switch {
	case message.Code == tea.KeyEscape,
		message.Code == 'n':
		m.side.confirm = nil
		m.side.notice = "Thread kept"
		m.refreshViewport(false)
		return m, nil, true
	case message.Code == tea.KeyEnter, message.Code == 'y':
		m.side.confirm = nil
		thread := m.side.thread(confirm.threadID)
		if thread == nil {
			return m, nil, true
		}
		if !thread.isRunning {
			updated, command := m.endSideThreadNow(thread.id)
			return updated, command, true
		}
		thread.closing = true
		m.side.notice = "Ending thread; cancelling its answer..."
		if thread.cancel != nil {
			thread.cancel()
		} else {
			thread.cancelPending = true
		}
		m.refreshViewport(false)
		return m, nil, true
	}
	return m, nil, true
}
