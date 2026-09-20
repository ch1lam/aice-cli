package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type inputDomain uint8

const (
	inputMain inputDomain = iota
	inputReading
	inputSessions
	inputGuard
	inputAuth
	inputSideMenu
	inputSideConfirm
	inputSide
	inputSecret
	inputCommand
)

// inputFocus names the active pane or interaction mode within a domain.
// It is derived from the owning component, never independently mutated.
type inputFocus uint8

const (
	inputFocusEditor inputFocus = iota
	inputFocusList
	inputFocusPreview
	inputFocusSearch
	inputFocusRename
	inputFocusWaiting
	inputFocusTranscript
	inputFocusDirectory
	inputFocusFeedback
)

// inputContext is a read-only projection, never a second owner of UI state.
// Operation limits (clipboard/delivery/run) remain on their existing owners.
type inputContext struct {
	domain inputDomain
	focus  inputFocus
	editor bool // the shared composer may receive remaining text/editing input
}

func (m model) inputContext() inputContext {
	switch {
	case m.reading != nil:
		focus := inputFocusTranscript
		if m.reading.directory {
			focus = inputFocusDirectory
		}
		return inputContext{domain: inputReading, focus: focus}
	case m.sessionPicker != nil:
		p := m.sessionPicker
		focus := inputFocusList
		switch {
		case p.restoring || p.rename != nil && p.rename.saving:
			focus = inputFocusWaiting
		case p.rename != nil:
			focus = inputFocusRename
		case p.input.Focused():
			focus = inputFocusSearch
		case p.previewFocused:
			focus = inputFocusPreview
		}
		return inputContext{domain: inputSessions, focus: focus}
	case m.guardPending != nil:
		focus := inputFocusList
		if m.guardFeedback {
			focus = inputFocusFeedback
		}
		return inputContext{domain: inputGuard, focus: focus}
	case m.authInput != nil:
		c := inputContext{domain: inputAuth, focus: inputFocusWaiting}
		if m.authPrompt != nil && !m.cancelRequested && !m.deliveryPending {
			if m.authPrompt.Menu != nil {
				c.focus = inputFocusList
			} else if m.authPrompt.AllowInput {
				c.focus, c.editor = inputFocusEditor, true
			}
		}
		return c
	case m.side.menu != nil:
		return inputContext{domain: inputSideMenu, focus: inputFocusList}
	case m.side.confirm != nil:
		return inputContext{domain: inputSideConfirm, focus: inputFocusList}
	case m.side.isVisible:
		return inputContext{domain: inputSide, editor: !m.deliveryPending && m.sideComposerEditable()}
	case m.secretInput != nil:
		return inputContext{domain: inputSecret, editor: !m.deliveryPending && !m.running}
	case m.commandMenu != nil:
		return inputContext{domain: inputCommand, focus: inputFocusList, editor: !m.deliveryPending && !m.running}
	default:
		return inputContext{domain: inputMain, editor: !m.deliveryPending && (!m.running || m.acceptsDelivery)}
	}
}

// identity distinguishes user-interaction lifetimes, not token/animation updates.
// Plain slash/file suggestions remain in the main input lifetime.
type inputIdentity struct {
	domain       inputDomain
	focus        inputFocus
	session      string
	side         uint64
	reading      *sessionReading
	picker       *sessionPicker
	rename       *sessionTitleEditor
	guard        *interaction.GuardRequest
	auth         chan string
	authPrompt   *interaction.AuthPrompt
	command      *commandMenuState
	commandDepth int
	secret       *secretInput
	menu         *sideMenuState
	confirm      *sideConfirmState
}

func (m model) inputIdentity() inputIdentity {
	c := m.inputContext()
	id := inputIdentity{domain: c.domain, session: m.sessionID}
	switch c.domain {
	case inputReading:
		id.reading = m.reading
	case inputSessions:
		id.picker, id.rename = m.sessionPicker, m.sessionPicker.rename
		id.focus = c.focus
	case inputGuard:
		id.guard = m.guardPending
		id.focus = c.focus
	case inputAuth:
		id.auth, id.authPrompt = m.authInput, m.authPrompt
	case inputCommand:
		id.command, id.commandDepth = m.commandMenu, len(m.commandMenu.frames)
	case inputSecret:
		id.secret = m.secretInput
	case inputSide, inputSideMenu, inputSideConfirm:
		id.side, id.menu, id.confirm = m.side.activeID, m.side.menu, m.side.confirm
	}
	return id
}

func (m model) composerInputEnabled() bool { return m.inputContext().editor }

// finishInputTransition runs for every Update return, including async takeovers.
// Commands returned by the event handler are preserved by the caller.
func (m *model) finishInputTransition(before inputIdentity) tea.Cmd {
	if before != m.inputIdentity() {
		m.inputGeneration++
		m.clearQuitPending = false
		m.cancelPointer()
		m.composerActive = false
		if m.clipboardPending {
			m.clipboardDiscard = true
		}
	}
	if !m.composerInputEnabled() {
		m.input.Blur()
	} else if !m.input.Focused() {
		return m.input.Focus()
	}
	return nil
}

// Bubbles has private asynchronous paste result types. Scope only editor
// commands so a delayed paste cannot cross a dialog or draft reset; domain
// results such as search/run/delivery retain their existing typed handlers.
type inputComponentResult struct {
	owner      inputIdentity
	generation uint64
	message    tea.Msg
}

func (m model) scopeInputCommand(command tea.Cmd) tea.Cmd {
	return scopeInputCommand(command, m.inputIdentity(), m.inputGeneration)
}

func scopeInputCommand(command tea.Cmd, owner inputIdentity, generation uint64) tea.Cmd {
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		message := command()
		if batch, ok := message.(tea.BatchMsg); ok {
			commands := make(tea.BatchMsg, len(batch))
			for i, child := range batch {
				commands[i] = scopeInputCommand(child, owner, generation)
			}
			return commands
		}
		return inputComponentResult{owner, generation, message}
	}
}
