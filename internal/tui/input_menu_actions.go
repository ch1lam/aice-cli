package tui

import tea "charm.land/bubbletea/v2"

const (
	inputActionCommands      inputAction = "commands"
	inputActionMenuMove      inputAction = "menu.move"
	inputActionMenuConfirm   inputAction = "menu.confirm"
	inputActionMenuCancel    inputAction = "menu.cancel"
	inputActionMenuComplete  inputAction = "menu.complete"
	inputActionSecretNewline inputAction = "secret.newline"
	inputActionFileMove      inputAction = "file.move"
	inputActionFileAttach    inputAction = "file.attach"
	inputActionFileExpand    inputAction = "file.expand"
	inputActionFileCancel    inputAction = "file.cancel"
	inputActionSlashMove     inputAction = "slash.move"
	inputActionSlashComplete inputAction = "slash.complete"
	inputActionSlashCancel   inputAction = "slash.cancel"
)

func menuMoveBindings(action inputAction) []inputBinding {
	up := actionBinding(action, "↑/↓", "select", "up")
	up.argument = -1
	down := actionBinding(action, "↑/↓", "select", "down")
	down.argument = 1
	return []inputBinding{up, down}
}

func (m model) sideMenuInputBindings() []inputBinding {
	bindings := []inputBinding{
		actionBinding(inputActionMenuConfirm, "Enter/Tab", "open", "enter", "tab"),
		actionBinding(inputActionMenuCancel, "Esc", "cancel", "esc"),
	}
	return append(bindings, menuMoveBindings(inputActionMenuMove)...)
}

func (m model) sideConfirmInputBindings() []inputBinding {
	return []inputBinding{
		actionBinding(inputActionMenuConfirm, "y/Enter", "end thread", "y", "enter"),
		actionBinding(inputActionMenuCancel, "n/Esc", "keep", "n", "esc"),
	}
}

func (m model) secretInputBindings() []inputBinding {
	send := inputBinding{action: inputActionSend, binding: m.keys.send, short: true}
	send.binding.SetHelp("Enter", "submit")
	send.binding.SetEnabled(m.composerInputEnabled())
	newline := inputBinding{action: inputActionSecretNewline, binding: m.keys.newline}
	newline.binding.SetHelp("", "")
	return []inputBinding{
		send,
		actionBinding(inputActionMenuCancel, "Esc", "cancel", "esc", "alt+esc", "ctrl+c", "ctrl+d"),
		newline,
	}
}

func (m model) handleSecretAction(match inputActionMatch) (model, tea.Cmd, bool) {
	switch match.action {
	case inputActionSend:
		return m.submitSecretInput()
	case inputActionMenuCancel:
		return m.cancelSecretInput()
	case inputActionSecretNewline:
		m.status = "API key must be entered on one line"
	}
	return m, nil, true
}

func (m model) commandInputBindings() []inputBinding {
	label := "close"
	if len(m.commandMenu.frames) > 1 {
		label = "back"
	}
	bindings := []inputBinding{
		actionBinding(inputActionMenuConfirm, "Enter", "choose", "enter"),
		actionBinding(inputActionMenuCancel, "Esc", label, "esc", "alt+esc", "ctrl+c", "ctrl+d"),
		actionBinding(inputActionMenuComplete, "Tab", "complete", "tab"),
	}
	bindings = append(bindings, menuMoveBindings(inputActionMenuMove)...)
	for i := range bindings {
		if bindings[i].action != inputActionMenuCancel {
			bindings[i].binding.SetEnabled(!m.running && !m.deliveryPending && len(m.matchingCommandOptions()) > 0)
		}
	}
	return bindings
}

func (m model) handleCommandAction(match inputActionMatch) (model, tea.Cmd, bool) {
	switch match.action {
	case inputActionMenuConfirm:
		return m.selectCommandMenuOption()
	case inputActionMenuCancel:
		return m.backOrCancelCommandMenu()
	case inputActionMenuComplete:
		return m.completeCommandMenuOption()
	case inputActionMenuMove:
		m.moveCommandMenuSelection(match.argument)
	}
	return m, nil, true
}

func (m model) completionInputBindings() []inputBinding {
	ref, ok := m.fileReferenceAtCursor()
	if ok && ref == m.fileCompletion.ref && !m.fileCompletion.dismissed && (m.fileCompletion.pending || m.fileCompletionVisible()) {
		bindings := []inputBinding{
			actionBinding(inputActionFileAttach, "Tab/Enter", "attach", "tab", "enter"),
			actionBinding(inputActionFileCancel, "Esc", "close", "esc"),
			actionBinding(inputActionFileExpand, "→", "expand", "right"),
		}
		bindings = append(bindings, menuMoveBindings(inputActionFileMove)...)
		for i := range bindings {
			if bindings[i].action != inputActionFileCancel {
				bindings[i].binding.SetEnabled(!m.fileCompletion.pending)
			}
			if m.clipboardPending && bindings[i].action == inputActionFileAttach {
				bindings[i].binding.SetEnabled(false)
				bindings[i].blocked = inputActionClipboardPending
			}
		}
		return bindings
	}
	if !m.slashCommandMenuVisible() {
		return nil
	}
	bindings := []inputBinding{
		actionBinding(inputActionSlashCancel, "Esc", "close", "esc"),
		actionBinding(inputActionSlashComplete, "Tab", "complete", "tab"),
	}
	if !m.hasExactSlashCommand() {
		enter := actionBinding(inputActionSlashComplete, "Enter", "choose", "enter")
		if m.clipboardPending {
			enter.binding.SetEnabled(false)
			enter.blocked = inputActionClipboardPending
		}
		bindings = append(bindings, enter)
	}
	return append(bindings, menuMoveBindings(inputActionSlashMove)...)
}

func (m model) handleCompletionAction(match inputActionMatch) (model, tea.Cmd, bool) {
	switch match.action {
	case inputActionFileMove:
		count := len(m.fileCompletion.items)
		m.fileCompletion.selection = (m.fileCompletion.selection + match.argument + count) % count
	case inputActionFileCancel:
		m.fileCompletion.dismissed = true
	case inputActionFileAttach:
		return m.applyFileCompletion(false)
	case inputActionFileExpand:
		return m.applyFileCompletion(true)
	case inputActionSlashMove:
		m.moveSlashCommandSelection(match.argument)
	case inputActionSlashComplete:
		m.completeSelectedSlashCommand()
		m.resizeLayout()
		return m, nil, true
	case inputActionSlashCancel:
		m.commandDismissed = true
	}
	m.resizeLayout()
	m.refreshViewport(false)
	return m, nil, true
}
