package tui

const (
	inputActionAuthCancel  inputAction = "auth.cancel"
	inputActionAuthSelect  inputAction = "auth.select"
	inputActionAuthChoose  inputAction = "auth.choose"
	inputActionAuthPage    inputAction = "auth.page"
	inputActionAuthSubmit  inputAction = "auth.submit"
	inputActionAuthNewline inputAction = "auth.newline"
)

func (m model) authInputBindings() []inputBinding {
	bindings := []inputBinding{
		actionBinding(inputActionAuthCancel, "Esc/Ctrl+c/Ctrl+d", "cancel", "esc", "ctrl+c", "ctrl+d"),
	}
	if m.authPrompt != nil && m.authPrompt.Menu != nil {
		options := m.authPrompt.Menu.Options
		choose := actionBinding(inputActionAuthChoose, "Enter", "select", m.keys.send.Keys()...)
		choose.binding.SetEnabled(len(options) > 0 && !m.cancelRequested)
		bindings = append(bindings, choose)
		for _, direction := range []struct {
			key   string
			delta int
		}{{key: "up", delta: -1}, {key: "down", delta: 1}} {
			binding := actionBinding(inputActionAuthSelect, "↑/↓", "navigate", direction.key)
			binding.argument = direction.delta
			binding.binding.SetEnabled(len(options) > 0 && !m.cancelRequested)
			bindings = append(bindings, binding)
		}
		return bindings
	}
	submit := actionBinding(inputActionAuthSubmit, "Enter", "submit", m.keys.send.Keys()...)
	submit.binding.SetEnabled(m.composerInputEnabled())
	newline := actionBinding(inputActionAuthNewline, "Shift+Enter", "newline", m.keys.newline.Keys()...)
	newline.binding.SetEnabled(false)
	bindings = append(bindings, submit, newline)
	for _, direction := range []struct {
		key   string
		delta int
	}{{key: "pgup", delta: -1}, {key: "pgdown", delta: 1}} {
		binding := actionBinding(inputActionAuthPage, "PgUp/PgDn", "scroll", direction.key)
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	return bindings
}
