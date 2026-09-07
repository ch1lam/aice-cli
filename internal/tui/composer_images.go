package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func (m model) applyClipboard(result clipboardResult) (tea.Model, tea.Cmd) {
	// A permission dialog or a side-thread update may have changed focus while
	// the native clipboard helper was running. Never paste into another input.
	if !m.composerInputEnabled() || m.secretInput != nil || m.commandMenu != nil || m.authInput != nil {
		return m, nil
	}
	if result.err == nil && result.image != nil {
		if m.side.isVisible {
			m.side.notice = "Image attachments are supported in the main conversation only"
			return m, nil
		}
		images := append(append([]llm.ImageContent(nil), m.images...), *result.image)
		result.err = interaction.ValidateImages(images)
		if result.err == nil {
			m.images = images
			m.inputNotice = ""
			m.historyIndex = -1
		}
	}
	var command tea.Cmd
	if result.err != nil {
		m.inputNotice = result.err.Error()
		if m.side.isVisible {
			m.side.notice = result.err.Error()
		}
	} else if result.image == nil && result.text != "" {
		m.inputNotice = ""
		command = m.updateInput(tea.PasteMsg{Content: result.text})
	} else if result.image == nil {
		m.inputNotice = "Clipboard contains no image or text"
	}
	m.resizeLayout()
	m.refreshViewport(false)
	return m, command
}

func imageInputText(text string, count int) string {
	for index := 0; index < count; index++ {
		if text != "" {
			text += "\n"
		}
		text += fmt.Sprintf("[Image %d]", index+1)
	}
	return text
}

func (m *model) restoreSubmittedInput() {
	if m.submittedInput == nil {
		return
	}
	input := *m.submittedInput
	m.submittedInput = nil
	m.images = input.Images
	m.input.SetValue(input.Prompt)
	m.input.CursorEnd()
	m.pastes = nil
	if len(input.Images) > 0 {
		m.inputNotice = "Input was not accepted; text and images restored"
	}
	// A rejected input was never added to Session history.
	if n := len(m.entries); n > 0 && m.entries[n-1].kind == entryUser &&
		strings.TrimSpace(m.entries[n-1].text) == strings.TrimSpace(imageInputText(input.Prompt, len(input.Images))) {
		m.entries = m.entries[:n-1]
	}
}
