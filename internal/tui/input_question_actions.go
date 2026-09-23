package tui

// questionInputBindings resolves the bottom Q&A panel controls. Single-letter
// answers (s/b) and digit selection stay out of the bindings on purpose: they
// are handled as text-routed shortcuts only while the text field is empty, so
// edited answers are never swallowed by option shortcuts.
func (m model) questionInputBindings() []inputBinding {
	if m.question == nil {
		return nil
	}
	confirm := actionBinding(inputActionQuestionConfirm, "Enter", "确认", "enter")
	tab := actionBinding(inputActionQuestionTab, "Tab", "切换输入", "tab", "shift+tab")
	skip := actionBinding(inputActionQuestionSkip, "Ctrl+s", "跳过", "ctrl+s")
	browse := actionBinding(inputActionQuestionBrowse, "Esc", "查看对话", "esc")
	cancel := actionBinding(inputActionQuestionCancel, "Ctrl+c", "取消运行", "ctrl+c")
	backspace := actionBinding(inputActionQuestionBackspace, "", "", "backspace")
	backspace.short = false
	newline := actionBinding(inputActionQuestionNewline, "Shift+Enter", "换行", "shift+enter")
	newline.short = false
	bindings := []inputBinding{confirm, tab, skip, browse, cancel, backspace, newline}
	for _, direction := range []struct {
		key   string
		delta int
	}{{key: "up", delta: -1}, {key: "down", delta: 1}} {
		binding := actionBinding(inputActionQuestionMove, "", "", direction.key)
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	for _, direction := range []struct {
		key   string
		delta int
	}{{key: "pgup", delta: -1}, {key: "pgdown", delta: 1}} {
		binding := actionBinding(inputActionQuestionScroll, "", "", direction.key)
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	return bindings
}
