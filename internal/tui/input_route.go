package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// handleKey resolves the domain before its reserved keys. An unconsumed key
// may reach only this domain's editor, never another domain's shortcuts.
func (m model) handleKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	c := m.inputContext()
	switch c.domain {
	case inputSettings:
		next, command := m.handleSettings(message)
		return next.(model), command, true
	case inputSessions:
		next, cmd := m.handleSessionPicker(message)
		return next.(model), cmd, true
	case inputMain:
		// Typing "/cmd " can open an option menu, which owns this key.
		m.syncCommandCompletion()
		c = m.inputContext()
	}
	match := m.matchInputAction(message)
	if match.matched {
		if !match.enabled {
			return m.blockedInputAction(match)
		}
		switch c.domain {
		case inputReading:
			next, cmd := m.handleReadingAction(match)
			return next.(model), cmd, true
		case inputGuard:
			return m.handleGuardAction(match)
		case inputQuestion:
			return m.handleQuestionAction(match)
		case inputAuth:
			return m.handleAuthAction(match)
		case inputSideMenu:
			return m.handleSideMenuAction(match)
		case inputSideConfirm:
			return m.handleSideConfirmAction(match)
		case inputCommand:
			return m.handleCommandAction(match)
		case inputSecret:
			return m.handleSecretAction(match)
		default:
			return m.handleComposerAction(match)
		}
	}
	switch {
	case c.domain == inputGuard:
		return m.handleGuardText(message)
	case c.domain == inputQuestion:
		return m.handleQuestionText(message)
	case m.deliveryPending:
		return m, nil, true
	}
	conversation := c.domain == inputMain || c.domain == inputSide
	if c.editor {
		if conversation {
			if updated, command, handled := m.handlePasteTokenKey(message); handled {
				updated.resizeLayout()
				return updated, command, true
			}
		}
		return m, nil, false
	}
	return m, nil, !conversation
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
	case inputSettings:
		return m.handleSettings(message)
	case inputSessions:
		return m.handleSessionPicker(message)
	case inputGuard, inputReading, inputSideMenu, inputSideConfirm:
		return m, nil
	case inputQuestion:
		updated, command := m.handleQuestionPaste(message.Content)
		return updated, command
	default:
		if m.composerInputEnabled() {
			command := m.updateInput(message)
			return m, command
		}
		return m, nil
	}
}

// The binding resolver owns availability and key meanings. This executor only
// applies an already-resolved action through the existing state owners.
func (m model) handleComposerAction(match inputActionMatch) (model, tea.Cmd, bool) {
	if m.deliveryPending {
		if m.cancelDelivery != nil {
			m.cancelDelivery()
		}
		return m, nil, true
	}
	switch match.action {
	case inputActionSessions:
		return m.openSessionPicker()
	case inputActionTurns:
		return m.openCurrentReading(), nil, true
	case inputActionPaste:
		m.clipboardRead = clipboardRead{pending: true, inSide: m.side.isVisible, sideID: m.side.activeID}
		return m, m.clipboard, true
	case inputActionClear:
		return m.clearInputOrQuit()
	case inputActionEditor:
		updated, command := m.openComposerEditor()
		return updated, command, true
	case inputActionCommands:
		return m, m.updateInput(tea.PasteMsg{Content: "/"}), true
	case inputActionHelp:
		m.help.ShowAll = !m.help.ShowAll
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	case inputActionScroll:
		if match.argument < 0 {
			m.viewport.PageUp()
		} else {
			m.viewport.PageDown()
		}
		return m, nil, true
	}
	if m.side.isVisible {
		return m.handleSideAction(match)
	}
	switch match.action {
	case inputActionHistory:
		return m.recallHistory(match.argument), nil, true
	case inputActionInterrupt:
		m.requestRunCancellation()
		return m, nil, true
	case inputActionQuit:
		return m, tea.Quit, true
	case inputActionCode:
		m.toggleVisibleCode()
	case inputActionProcess:
		follow := m.viewport.AtBottom()
		m.toggleProcessGroups()
		m.refreshViewport(follow)
	case inputActionQueue:
		if m.isBTWCommandInput() || m.isInfoCommandInput() {
			return m.submit()
		}
		return m.submitDelivery(deliveryQueue)
	case inputActionNewline:
		return m, m.updateInput(tea.PasteMsg{Content: "\n"}), true
	case inputActionSend:
		if m.running {
			if m.isBTWCommandInput() || m.isInfoCommandInput() {
				return m.submit()
			}
			return m.submitDelivery(deliverySteer)
		}
		return m.submit()
	default:
		return m.handleCompletionAction(match)
	}
	return m, nil, true
}

// Settings and the main composer use the same cancellation chain, including
// the interval before the controller publishes its cancel function.
func (m *model) requestRunCancellation() {
	if !m.running || m.cancelRequested {
		return
	}
	m.cancelRequested = true
	if m.cancelRun != nil {
		m.cancelRun()
	}
	m.status = "Cancelling current response..."
}
