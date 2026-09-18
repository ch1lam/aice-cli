package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

type sessionTitleEditor struct {
	key    string
	input  textinput.Model
	saving bool
}

type sessionRenameResult struct {
	generation uint64
	item       interaction.SessionSummary
	err        error
}

func (m *model) openSessionTitleEditor() tea.Cmd {
	p := m.sessionPicker
	item, ok := p.list.SelectedItem().(sessionListItem)
	if !ok || item.Problem != "" || m.renameSession == nil {
		return nil
	}
	if p.cancelSearch != nil {
		p.cancelSearch()
	}
	if p.cancelPreview != nil {
		p.cancelPreview()
	}
	m.sessionQueryGeneration++
	m.sessionPreviewGeneration++
	p.loading, p.notice = false, ""
	input := textinput.New()
	input.Prompt, input.Placeholder, input.CharLimit = "› ", "Automatic title", 200
	input.SetVirtualCursor(false)
	input.SetValue(item.Title)
	input.CursorEnd()
	p.input.Blur()
	p.rename = &sessionTitleEditor{key: item.Key, input: input}
	m.resizeSessionPicker()
	return p.rename.input.Focus()
}

func (m model) handleSessionTitleEditor(message tea.Msg) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	editor := p.rename
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc", "ctrl+c":
			if p.cancelRename != nil {
				p.cancelRename()
			}
			p.rename = nil
			p.previewFocused = false
			return m, tea.Batch(p.input.Focus(), m.requestSessionSearch(), m.requestSessionPreview())
		case "enter":
			if editor.saving {
				return m, nil
			}
			editor.saving = true
			editor.input.Blur()
			p.notice = ""
			command, cancel := m.renameSession(m.sessionQueryGeneration, editor.key, editor.input.Value())
			p.cancelRename = cancel
			return m, command
		}
	}
	if editor.saving {
		return m, nil
	}
	var command tea.Cmd
	editor.input, command = editor.input.Update(message)
	return m, command
}

func (m model) applySessionRename(result sessionRenameResult) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	if p == nil || p.rename == nil || result.generation != m.sessionQueryGeneration {
		return m, nil
	}
	if result.err != nil {
		p.rename.saving = false
		p.notice = result.err.Error()
		return m, p.rename.input.Focus()
	}
	items := append([]interaction.SessionSummary(nil), p.all...)
	found := false
	for i := range items {
		if items[i].Key == result.item.Key {
			items[i], found = result.item, true
			break
		}
	}
	if !found {
		items = append(items, result.item)
	}
	p.all, p.rename, p.previewFocused = items, nil, false
	// Return to recent sessions so a changed title cannot vanish under the old
	// title query. setSessionItems preserves the selected file identity.
	p.input.SetValue("")
	m.setSessionItems(items)
	return m, tea.Batch(p.input.Focus(), m.requestSessionSearch(), m.requestSessionPreview())
}
