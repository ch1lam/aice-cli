package tui

import tea "charm.land/bubbletea/v2"

const (
	inputActionSessionSearch        inputAction = "session.search"
	inputActionSessionClose         inputAction = "session.close"
	inputActionSessionHidePreview   inputAction = "session.hide-preview"
	inputActionSessionPreview       inputAction = "session.preview"
	inputActionSessionList          inputAction = "session.list"
	inputActionSessionRename        inputAction = "session.rename"
	inputActionSessionRead          inputAction = "session.read"
	inputActionSessionResume        inputAction = "session.resume"
	inputActionSessionToggleGroup   inputAction = "session.toggle-group"
	inputActionSessionMove          inputAction = "session.move"
	inputActionSessionPage          inputAction = "session.page"
	inputActionSessionCancelRestore inputAction = "session.cancel-restore"
	inputActionSessionCancelRename  inputAction = "session.cancel-rename"
	inputActionSessionSaveTitle     inputAction = "session.save-title"
)

// The picker owns search, list and preview focus. Only search/title editors
// receive unmatched text; every reserved action also supplies its help label.
func (m model) sessionInputBindings() []inputBinding {
	p := m.sessionPicker
	if p.rename != nil {
		save := actionBinding(inputActionSessionSaveTitle, "Enter", "save", "enter")
		save.binding.SetEnabled(!p.rename.saving)
		return []inputBinding{
			actionBinding(inputActionSessionCancelRename, "Esc", "cancel", "esc", "ctrl+c"),
			save,
		}
	}
	if p.restoring {
		return []inputBinding{actionBinding(inputActionSessionCancelRestore, "Esc", "cancel restore", "esc", "ctrl+c")}
	}

	var bindings []inputBinding
	if p.previewVisible {
		bindings = append(bindings, actionBinding(inputActionSessionHidePreview, "Esc", "hide preview", "esc"))
		close := actionBinding(inputActionSessionClose, "Ctrl+C", "close", "ctrl+c")
		close.short = false
		bindings = append(bindings, close)
	} else {
		bindings = append(bindings, actionBinding(inputActionSessionClose, "Esc", "close", "esc", "ctrl+c"))
	}
	item, selected := p.list.SelectedItem().(sessionListItem)
	if group, ok := p.list.SelectedItem().(sessionGroupItem); ok {
		bindings = append(bindings, sessionGroupInputBinding(group))
	} else {
		resume := actionBinding(inputActionSessionResume, "Enter", "resume", "enter")
		resume.binding.SetEnabled(selected && item.Problem == "" && (item.current || (!m.running && !m.side.anyRunning())))
		bindings = append(bindings, resume)
	}
	if m.inputContext().focus != inputFocusSearch {
		bindings = append(bindings, sessionSearchInputBinding())
	}
	preview := actionBinding(inputActionSessionPreview, "→", "preview", "right")
	if p.previewVisible {
		preview.binding.SetHelp("←→", "focus")
		bindings = append(bindings, actionBinding(inputActionSessionList, "←→", "focus", "left"))
	}
	bindings = append(bindings, preview)

	movement := "select"
	if m.inputContext().focus == inputFocusPreview {
		movement = "scroll"
	}
	for _, direction := range []struct {
		key, alternate string
		step           int
	}{
		{"up", "ctrl+p", -1},
		{"down", "ctrl+n", 1},
	} {
		binding := actionBinding(inputActionSessionMove, "↑↓", movement, direction.key, direction.alternate)
		binding.argument = direction.step
		bindings = append(bindings, binding)
	}
	for _, direction := range []struct {
		label, key string
		step       int
	}{
		{"PgUp", "pgup", -1},
		{"PgDown", "pgdown", 1},
	} {
		binding := actionBinding(inputActionSessionPage, direction.label, "page", direction.key)
		binding.argument, binding.short = direction.step, false
		bindings = append(bindings, binding)
	}
	rename := actionBinding(inputActionSessionRename, "F2", "rename", "f2")
	rename.binding.SetEnabled(selected && item.Problem == "" && m.renameSession != nil)
	read := actionBinding(inputActionSessionRead, "F4", "read", "f4")
	read.binding.SetEnabled(selected && item.Problem == "" && m.readSession != nil)
	return append(bindings, rename, read)
}

func sessionSearchInputBinding() inputBinding {
	return actionBinding(inputActionSessionSearch, "/", "search", "/")
}

func sessionGroupInputBinding(group sessionGroupItem) inputBinding {
	description := "collapse group"
	if group.collapsed {
		description = "expand group"
	}
	return actionBinding(inputActionSessionToggleGroup, "Enter", description, "enter")
}

func (m model) handleSessionAction(match inputActionMatch) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	switch match.action {
	case inputActionSessionSearch:
		p.previewFocused = false
		return m, p.input.Focus()
	case inputActionSessionClose:
		return m, m.closeSessionPicker()
	case inputActionSessionHidePreview:
		return m, m.toggleSessionPreview()
	case inputActionSessionPreview:
		if !p.previewVisible {
			return m, m.toggleSessionPreview()
		}
		p.previewFocused = true
		p.input.Blur()
	case inputActionSessionList:
		p.previewFocused = false
		p.input.Blur()
	case inputActionSessionRename:
		return m, m.openSessionTitleEditor()
	case inputActionSessionRead:
		return m, m.requestSessionReading()
	case inputActionSessionResume:
		return m.resumeSelectedSession()
	case inputActionSessionToggleGroup:
		return m, m.toggleSessionGroup()
	case inputActionSessionMove, inputActionSessionPage:
		return m, m.moveSessionSelection(match)
	case inputActionSessionCancelRestore:
		if m.cancelRun != nil {
			m.cancelRun()
		} else {
			m.cancelRequested = true
		}
	case inputActionSessionCancelRename:
		if p.cancelRename != nil {
			p.cancelRename()
		}
		p.rename = nil
		p.previewFocused = false
		return m, tea.Batch(p.input.Focus(), m.requestSessionSearch(), m.requestSessionPreview())
	case inputActionSessionSaveTitle:
		editor := p.rename
		editor.saving = true
		editor.input.Blur()
		p.notice = ""
		command, cancel := m.renameSession(m.sessionQueryGeneration, editor.key, editor.input.Value())
		p.cancelRename = cancel
		return m, command
	}
	return m, nil
}

func (m *model) moveSessionSelection(match inputActionMatch) tea.Cmd {
	p := m.sessionPicker
	if m.inputContext().focus == inputFocusPreview {
		switch {
		case match.action == inputActionSessionMove && match.argument < 0:
			p.preview.ScrollUp(1)
		case match.action == inputActionSessionMove:
			p.preview.ScrollDown(1)
		case match.argument < 0:
			p.preview.PageUp()
		default:
			p.preview.PageDown()
		}
		return nil
	}
	p.input.Blur()
	old := p.list.Index()
	switch {
	case match.action == inputActionSessionMove && match.argument < 0:
		p.list.CursorUp()
	case match.action == inputActionSessionMove:
		p.list.CursorDown()
	case match.argument < 0:
		p.list.PrevPage()
	default:
		p.list.NextPage()
	}
	if old != p.list.Index() {
		return m.requestSessionPreview()
	}
	return nil
}
