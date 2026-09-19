package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func (m model) handleKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if message.String() == "ctrl+t" && !m.running && !m.side.anyRunning() && !m.side.isVisible && !m.clipboardPending && !m.deliveryPending && m.side.menu == nil && m.side.confirm == nil && m.guardPending == nil && m.commandMenu == nil && m.secretInput == nil && m.authInput == nil {
		return m.openCurrentReading(), nil, true
	}
	if message.String() == "ctrl+r" && m.searchSessions != nil && !m.side.isVisible &&
		m.guardPending == nil && m.authInput == nil && m.secretInput == nil && m.commandMenu == nil &&
		m.side.menu == nil && m.side.confirm == nil && !m.clipboardPending && !m.deliveryPending {
		return m.openSessionPicker()
	}
	if m.commandMenu == nil {
		m.syncCommandCompletion()
	}
	if m.clearQuitPending && (!key.Matches(message, m.keys.clear) ||
		m.guardPending != nil || m.authInput != nil || m.secretInput != nil ||
		m.commandMenu != nil || m.side.menu != nil || m.side.confirm != nil) {
		m.clearQuitPending = false
		m.resizeLayout()
	}
	if m.guardPending != nil {
		updated, cmd, handled := m.handleGuardKey(message)
		if handled {
			return updated, cmd, true
		}
		return m, nil, true
	}
	if m.authInput != nil {
		return m.handleAuthKey(message)
	}
	if m.side.menu != nil {
		return m.handleSideMenuKey(message)
	}
	if m.side.confirm != nil {
		return m.handleSideConfirmKey(message)
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
		if updated, command, handled := m.handleSideKey(message); handled {
			return updated, command, true
		}
		// The side panel owns all keyboard input while visible. Unhandled keys
		// fall through to its textarea in Update, never to main-run shortcuts or
		// prompt-history navigation.
		return m, nil, false
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
			m.input.InsertString("\n")
			m.resizeLayout()
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
