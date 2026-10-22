package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Authentication input belongs to the pending command, never to prompt history.
func (m model) handleAuthAction(match inputActionMatch) (model, tea.Cmd, bool) {
	switch match.action {
	case inputActionAuthCancel:
		m.cancelRequested = true
		if m.cancelRun != nil {
			m.cancelRun()
		}
		m.input.Reset()
		m.input.Blur()
		m.status = "Cancelling command..."
		return m.settleCommand(false, nil)
	case inputActionAuthSelect:
		options := m.flow.prompt.Menu.Options
		m.flow.selection = (m.flow.selection + len(options) + match.argument) % len(options)
		return m.settleCommand(true, nil)
	case inputActionAuthChoose:
		options := m.flow.prompt.Menu.Options
		if m.flow.reply(options[m.flow.selection].Arguments) {
			m.flow.prompt = nil
			m.input.Blur()
			m.status = "Working..."
		}
		return m.settleCommand(true, nil)
	case inputActionAuthPage:
		if match.argument < 0 {
			m.viewport.PageUp()
		} else {
			m.viewport.PageDown()
		}
		return m, nil, true
	case inputActionAuthSubmit:
		if value := strings.TrimSpace(m.input.Value()); value != "" {
			if !m.flow.reply(value) {
				m.status = "Still checking authorization; please wait"
				return m.settleCommand(false, nil)
			}
			m.input.Reset()
			m.status = "Checking authorization..."
			if name := m.flow.command.Name; name != "login" {
				// Interactive command prompts are single-shot: the command
				// sends the next prompt itself.
				m.flow.prompt = nil
				m.input.Blur()
				m.status = "Running /" + name + "..."
				if name == "browser" {
					m.status = "Connecting browser..."
				}
			}
		}
		return m.settleCommand(false, nil)
	}
	return m, nil, true
}

func (m model) authView() string {
	prompt := m.flow.prompt
	link := ansi.SetHyperlink(prompt.URL) + prompt.URL + ansi.SetHyperlink("")
	parts := []string{prompt.Title, link}
	if prompt.Code != "" {
		parts = append(parts, "Enter code: "+prompt.Code)
	}
	parts = append(parts, prompt.Instructions)
	if prompt.Menu != nil {
		for i, option := range prompt.Menu.Options {
			prefix := "  "
			if i == m.flow.selection {
				prefix = "› "
			}
			parts = append(parts, prefix+ansi.Strip(option.Label))
		}
	}
	return strings.Join(parts, "\n\n")
}
