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

// inputContext is a read-only projection, never a second owner of UI state.
// Operation limits (clipboard/delivery/run) remain on their existing owners.
type inputContext struct {
	domain inputDomain
	editor bool // the shared composer may receive remaining text/editing input
}

func (m model) inputContext() inputContext {
	switch {
	case m.reading != nil:
		return inputContext{domain: inputReading}
	case m.sessionPicker != nil:
		return inputContext{domain: inputSessions}
	case m.guardPending != nil:
		return inputContext{domain: inputGuard}
	case m.authInput != nil:
		return inputContext{inputAuth, m.authPrompt != nil && m.authPrompt.AllowInput && m.authPrompt.Menu == nil && !m.cancelRequested && !m.deliveryPending}
	case m.side.menu != nil:
		return inputContext{domain: inputSideMenu}
	case m.side.confirm != nil:
		return inputContext{domain: inputSideConfirm}
	case m.side.isVisible:
		return inputContext{inputSide, !m.deliveryPending && m.sideComposerEditable()}
	case m.secretInput != nil:
		return inputContext{inputSecret, !m.deliveryPending && !m.running}
	case m.commandMenu != nil:
		return inputContext{inputCommand, !m.deliveryPending && !m.running}
	default:
		return inputContext{inputMain, !m.deliveryPending && (!m.running || m.acceptsDelivery)}
	}
}

// identity distinguishes user-interaction lifetimes, not token/animation updates.
// Plain slash/file suggestions remain in the main input lifetime.
type inputIdentity struct {
	domain       inputDomain
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
	case inputGuard:
		id.guard = m.guardPending
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
