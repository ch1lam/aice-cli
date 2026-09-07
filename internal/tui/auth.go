package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

// Authentication input belongs to the pending command, never to prompt history.
func (m model) handleAuthKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if cancelKeyPressed(message, m.keys) {
		m.cancelRequested = true
		if m.cancelRun != nil {
			m.cancelRun()
		}
		m.input.Reset()
		m.input.Blur()
		m.status = "Cancelling login..."
		return m.settleCommand(false, nil)
	}
	if message.Code == tea.KeyPgUp || message.Code == tea.KeyPgDown {
		var command tea.Cmd
		m.viewport, command = m.viewport.Update(message)
		return m, command, true
	}
	if !m.composerInputEnabled() {
		return m, nil, true
	}
	if key.Matches(message, m.keys.send) {
		if value := strings.TrimSpace(m.input.Value()); value != "" {
			select {
			case m.authInput <- value:
				m.input.Reset()
				m.status = "Checking authorization..."
			default:
				m.status = "Still checking authorization; please wait"
			}
		}
		return m.settleCommand(false, nil)
	}
	if key.Matches(message, m.keys.newline) {
		return m, nil, true
	}
	command := m.updateInput(message)
	return m, command, true
}

func (m model) authView() string {
	prompt := m.authPrompt
	link := ansi.SetHyperlink(prompt.URL) + prompt.URL + ansi.SetHyperlink("")
	parts := []string{prompt.Title, link}
	if prompt.Code != "" {
		parts = append(parts, "Enter code: "+prompt.Code)
	}
	parts = append(parts, prompt.Instructions)
	return strings.Join(parts, "\n\n")
}
