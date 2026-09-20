package tui

const (
	inputActionTrustPrevious inputAction = "trust.previous"
	inputActionTrustNext     inputAction = "trust.next"
	inputActionTrustSelect   inputAction = "trust.select"
	inputActionTrustCancel   inputAction = "trust.cancel"
)

func (m trustPromptModel) inputBindings() []inputBinding {
	bindings := []inputBinding{
		actionBinding(inputActionTrustSelect, "Enter", "select", "enter"),
		actionBinding(inputActionTrustCancel, "Esc/Ctrl+c", "cancel", "esc", "ctrl+c"),
		actionBinding(inputActionTrustPrevious, "", "", "up"),
		actionBinding(inputActionTrustNext, "", "", "down"),
	}
	for i := range bindings {
		if bindings[i].action != inputActionTrustCancel {
			bindings[i].binding.SetEnabled(m.selected >= 0 && m.selected < len(m.choices))
		}
	}
	return bindings
}
