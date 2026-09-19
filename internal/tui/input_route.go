package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// handleKey resolves the domain before its reserved keys. An unconsumed key
// may reach only this domain's editor, never another domain's shortcuts.
func (m model) handleKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.inputContext().domain == inputMain {
		m.syncCommandCompletion()
	}
	if match := m.matchInputAction(message); match.matched && !match.enabled {
		return m.blockedInputAction(match)
	}
	switch m.inputContext().domain {
	case inputReading:
		next, cmd := m.handleReadingKey(message)
		return next.(model), cmd, true
	case inputSessions:
		next, cmd := m.handleSessionPicker(message)
		return next.(model), cmd, true
	case inputGuard:
		return m.handleGuardKey(message)
	case inputAuth:
		return m.handleAuthKey(message)
	case inputSideMenu:
		return m.handleSideMenuKey(message)
	case inputSideConfirm:
		return m.handleSideConfirmKey(message)
	default:
		return m.handleComposerKey(message)
	}
}

func (m model) routeKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.clearQuitPending && !key.Matches(message, m.keys.clear) {
		m.clearQuitPending = false
		m.resizeLayout()
	}
	m.selection.clear()
	if message.Code == tea.KeyEscape {
		m.composerActive = false
	} else if m.composerInputEnabled() && m.input.Focused() {
		switch message.Code {
		case tea.KeyBackspace, tea.KeyDelete, tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd, tea.KeyEnter:
			m.composerActive = true
		default:
			if message.Text != "" {
				m.composerActive = true
			}
		}
	}
	updated, command, handled := m.handleKey(message)
	m = updated
	if handled {
		if key.Matches(message, m.keys.send, m.keys.queue) && m.input.Value() == "" {
			m.composerActive = false
		}
		return m, command
	}
	if m.composerInputEnabled() {
		edit := m.updateInput(message)
		return m, tea.Batch(command, edit)
	}

	return m, command
}

func (m model) routePaste(message tea.PasteMsg) (tea.Model, tea.Cmd) {
	m.clearQuitPending = false
	switch m.inputContext().domain {
	case inputSessions:
		return m.handleSessionPicker(message)
	case inputGuard, inputReading, inputSideMenu, inputSideConfirm:
		return m, nil
	default:
		if m.composerInputEnabled() {
			command := m.updateInput(message)
			return m, command
		}
		return m, nil
	}
}

func (m model) handleComposerKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if message.String() == "ctrl+t" && !m.running && !m.side.anyRunning() && !m.side.isVisible && !m.clipboardPending && !m.deliveryPending && m.side.menu == nil && m.side.confirm == nil && m.guardPending == nil && m.commandMenu == nil && m.secretInput == nil && m.authInput == nil {
		return m.openCurrentReading(), nil, true
	}
	if message.String() == "ctrl+r" && m.searchSessions != nil && !m.side.isVisible &&
		m.guardPending == nil && m.authInput == nil && m.secretInput == nil && m.commandMenu == nil &&
		m.side.menu == nil && m.side.confirm == nil && !m.clipboardPending && !m.deliveryPending {
		return m.openSessionPicker()
	}
	if m.clearQuitPending && (!key.Matches(message, m.keys.clear) ||
		m.guardPending != nil || m.authInput != nil || m.secretInput != nil ||
		m.commandMenu != nil || m.side.menu != nil || m.side.confirm != nil) {
		m.clearQuitPending = false
		m.resizeLayout()
	}
	if m.deliveryPending {
		if cancelKeyPressed(message, m.keys) && m.cancelDelivery != nil {
			m.cancelDelivery()
		}
		return m, nil, true
	}
	if m.clipboardPending && (key.Matches(message, m.keys.paste) ||
		key.Matches(message, m.keys.send) || key.Matches(message, m.keys.queue) ||
		key.Matches(message, m.keys.editor)) {
		m.inputNotice = "Reading clipboard; press Enter again when the attachment appears"
		if m.side.isVisible {
			m.side.notice = "Reading clipboard; press Enter again when ready"
		}
		return m.settleCommand(false, nil)
	}
	if m.composerInputEnabled() && m.secretInput == nil && m.commandMenu == nil {
		if key.Matches(message, m.keys.paste) && m.clipboard != nil {
			m.clipboardPending = true
			m.clipboardInSide = m.side.isVisible
			m.clipboardSideID = m.side.activeID
			return m, m.clipboard, true
		}
	}
	if m.secretInput == nil && m.commandMenu == nil && key.Matches(message, m.keys.clear) {
		return m.clearInputOrQuit()
	}
	if m.side.isVisible {
		// The side domain owns unhandled editing input and any auxiliary command.
		return m.handleSideKey(message)
	}

	if !m.running && m.secretInput != nil {
		switch {
		case cancelKeyPressed(message, m.keys):
			return m.cancelSecretInput()
		case key.Matches(message, m.keys.newline):
			m.status = "API key must be entered on one line"
			return m, nil, true
		}
	}

	if !m.running && m.commandMenu != nil {
		switch {
		case cancelKeyPressed(message, m.keys):
			return m.backOrCancelCommandMenu()
		case message.Code == tea.KeyUp:
			m.moveCommandMenuSelection(-1)
			return m, nil, true
		case message.Code == tea.KeyDown:
			m.moveCommandMenuSelection(1)
			return m, nil, true
		case message.Code == tea.KeyTab:
			return m.completeCommandMenuOption()
		case key.Matches(message, m.keys.send):
			return m.selectCommandMenuOption()
		default:
			return m, nil, false
		}
	}

	if m.secretInput == nil &&
		m.commandMenu == nil &&
		m.composerInputEnabled() {
		// Ctrl+G edits the composer in the default editor; placeholder
		// tokens stay atomic for cursor motion and deletion.
		if !m.running && key.Matches(message, m.keys.editor) {
			updated, command := m.openComposerEditor()
			return updated, command, true
		}
		if updated, command, handled := m.handlePasteTokenKey(message); handled {
			updated.resizeLayout()
			return updated, command, true
		}
	}

	if updated, command, handled := m.handleFileCompletionKey(message); handled {
		return updated, command, true
	}
	if !m.running && m.slashCommandMenuVisible() {
		switch message.Code {
		case tea.KeyUp:
			m.moveSlashCommandSelection(-1)
			return m, nil, true
		case tea.KeyDown:
			m.moveSlashCommandSelection(1)
			return m, nil, true
		case tea.KeyTab:
			m.completeSelectedSlashCommand()
			m.resizeLayout()
			return m, nil, true
		case tea.KeyEscape:
			m.commandDismissed = true
			m.resizeLayout()
			m.refreshViewport(false)
			return m, nil, true
		}
	}

	if !m.running &&
		m.secretInput == nil &&
		m.commandMenu == nil &&
		!m.slashCommandMenuVisible() {
		// Up recalls an earlier prompt, Down moves forward again. A multi-line
		// draft never switches (arrow keys keep editing its lines); switching
		// resumes once an entry has been recalled, even when that entry is
		// itself multi-line.
		if message.Code == tea.KeyUp && m.historyBackAllowed() {
			return m.recallHistory(-1), nil, true
		}
		if message.Code == tea.KeyDown && m.historyForwardAllowed() {
			return m.recallHistory(1), nil, true
		}
	}

	switch {
	case key.Matches(message, m.keys.interrupt):
		if m.running {
			if m.cancelRun != nil {
				m.cancelRun()
			} else {
				m.cancelRequested = true
			}
			m.status = "Cancelling current response..."
			return m, nil, true
		}
		return m, nil, true
	case key.Matches(message, m.keys.quit):
		if !m.running && strings.TrimSpace(m.expandComposerText()) == "" && len(m.composerImages()) == 0 {
			return m, tea.Quit, true
		}
		return m, nil, true
	case m.helpToggleRequested(message):
		m.help.ShowAll = !m.help.ShowAll
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	case key.Matches(message, m.keys.code):
		m.toggleVisibleCode()
		return m, nil, true
	case key.Matches(message, m.keys.process):
		follow := m.viewport.AtBottom()
		m.toggleProcessGroups()
		m.refreshViewport(follow)
		return m, nil, true
	case key.Matches(message, m.keys.queue):
		if m.running {
			if m.isBTWCommandInput() {
				return m.submit()
			}
			if m.acceptsDelivery {
				return m.submitDelivery(deliveryQueue)
			}
			m.status = "Current command is still running"
			return m, nil, true
		}
		return m, nil, true
	case key.Matches(message, m.keys.newline):
		if m.composerInputEnabled() {
			command := m.updateInput(tea.PasteMsg{Content: "\n"})
			return m, command, true
		}
		return m, nil, true
	case key.Matches(message, m.keys.send):
		if m.running {
			if m.isBTWCommandInput() {
				return m.submit()
			}
			if m.acceptsDelivery {
				return m.submitDelivery(deliverySteer)
			}
			m.status = "Current command is still running"
			return m, nil, true
		}
		if m.slashCommandMenuVisible() && !m.hasExactSlashCommand() {
			m.completeSelectedSlashCommand()
			m.resizeLayout()
			return m, nil, true
		}
		return m.submit()
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
