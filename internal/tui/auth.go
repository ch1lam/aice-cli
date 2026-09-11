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
		m.status = "Cancelling command..."
		return m.settleCommand(false, nil)
	}
	if m.authPrompt != nil && m.authPrompt.Menu != nil {
		options := m.authPrompt.Menu.Options
		if len(options) == 0 {
			return m, nil, true
		}
		switch message.Code {
		case tea.KeyUp:
			m.authSelection = (m.authSelection + len(options) - 1) % len(options)
		case tea.KeyDown:
			m.authSelection = (m.authSelection + 1) % len(options)
		case tea.KeyEnter:
			select {
			case m.authInput <- options[m.authSelection].Arguments:
				m.authPrompt = nil
				m.input.Blur()
				m.status = "Working..."
			default:
			}
		}
		return m.settleCommand(true, nil)
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
				if m.authCommand == "browser" {
					m.authPrompt = nil
					m.input.Blur()
					m.status = "Connecting browser..."
				}
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
	if prompt.Menu != nil {
		for i, option := range prompt.Menu.Options {
			prefix := "  "
			if i == m.authSelection {
				prefix = "› "
			}
			parts = append(parts, prefix+ansi.Strip(option.Label))
		}
	}
	return strings.Join(parts, "\n\n")
}
