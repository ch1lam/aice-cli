package tui

// questionInputBindings resolves the bottom Q&A panel controls. The panel
// submits all questions at once: Space (or a digit) selects an option for
// the current question and stays there, Left/Right switch questions, and
// Enter submits the whole group. Space stays a text key while typing, on
// an empty custom row, or for free-text questions, so answers containing
// spaces remain typable. A populated custom row can be selected with Space
// after moving focus back to it.
func (m model) questionInputBindings() []inputBinding {
	if m.question == nil {
		return nil
	}
	submit := actionBinding(inputActionQuestionSubmit, "Enter", "提交全部", "enter")
	browse := actionBinding(inputActionQuestionBrowse, "Esc", "查看对话", "esc")
	cancel := actionBinding(inputActionQuestionCancel, "Ctrl+c", "取消运行", "ctrl+c")
	backspace := actionBinding(inputActionQuestionBackspace, "", "", "backspace")
	backspace.short = false
	newline := actionBinding(inputActionQuestionNewline, "Shift+Enter", "换行", "shift+enter")
	newline.short = false
	bindings := []inputBinding{submit, browse, cancel, backspace, newline}
	if m.question.shouldSpaceSelect() {
		selectBinding := actionBinding(inputActionQuestionSelect, "Space", "选中", "space", " ")
		bindings = append(bindings, selectBinding)
	}
	for _, direction := range []struct {
		key   string
		delta int
	}{{key: "up", delta: -1}, {key: "down", delta: 1}} {
		binding := actionBinding(inputActionQuestionMove, "", "", direction.key)
		if m.question.hasOptions() {
			binding.binding.SetHelp("↑↓", "选择")
		}
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	for _, direction := range []struct {
		key   string
		delta int
	}{{key: "left", delta: -1}, {key: "right", delta: 1}} {
		binding := actionBinding(inputActionQuestionSwitch, "", "", direction.key)
		if len(m.question.prompt.Request.Questions) > 1 {
			binding.binding.SetHelp("←→", "切题")
		}
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	for _, direction := range []struct {
		key   string
		delta int
	}{{key: "pgup", delta: -1}, {key: "pgdown", delta: 1}} {
		binding := actionBinding(inputActionQuestionScroll, "PgUp/PgDn", "滚动", direction.key)
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	return bindings
}
