package tui

import "strconv"

const (
	inputActionGuardPage      inputAction = "guard.page"
	inputActionGuardBoundary  inputAction = "guard.boundary"
	inputActionGuardSelect    inputAction = "guard.select"
	inputActionGuardConfirm   inputAction = "guard.confirm"
	inputActionGuardOption    inputAction = "guard.option"
	inputActionGuardSubmit    inputAction = "guard.submit"
	inputActionGuardBack      inputAction = "guard.back"
	inputActionGuardBackspace inputAction = "guard.backspace"
)

func (m model) guardInputBindings() []inputBinding {
	if m.guardPending == nil {
		return nil
	}
	var bindings []inputBinding
	if m.inputContext().focus == inputFocusFeedback {
		bindings = append(bindings,
			actionBinding(inputActionGuardSubmit, "Enter", "send", "enter"),
			actionBinding(inputActionGuardBack, "Esc", "back", "esc"),
		)
		backspace := actionBinding(inputActionGuardBackspace, "Backspace", "delete", "backspace")
		backspace.short = false
		bindings = append(bindings, backspace)
	} else {
		confirm := actionBinding(inputActionGuardConfirm, "Enter", "confirm", "enter")
		confirm.binding.SetEnabled(m.guardSelection >= 0 && m.guardSelection < len(m.guardPending.Options))
		deny := actionBinding(inputActionGuardOption, "n/Esc", "deny", "n", "N", "esc")
		deny.argument = firstDenyGuardOption(m.guardPending.Options)
		deny.binding.SetEnabled(deny.argument >= 0)
		bindings = append(bindings, confirm, deny)
		for _, direction := range []struct {
			key   string
			delta int
		}{{key: "up", delta: -1}, {key: "down", delta: 1}} {
			binding := actionBinding(inputActionGuardSelect, "↑/↓", "select", direction.key)
			binding.argument = direction.delta
			binding.binding.SetEnabled(len(m.guardPending.Options) > 0)
			bindings = append(bindings, binding)
		}
		first := actionBinding(inputActionGuardOption, "y", "first", "y", "Y")
		first.binding.SetEnabled(len(m.guardPending.Options) > 0)
		first.short = false
		bindings = append(bindings, first)
		for index := range min(len(m.guardPending.Options), 9) {
			label := strconv.Itoa(index + 1)
			binding := actionBinding(inputActionGuardOption, label, "confirm option", label)
			binding.argument = index
			binding.short = false
			bindings = append(bindings, binding)
		}
	}
	for _, direction := range []struct {
		key, label string
		action     inputAction
		delta      int
	}{
		{key: "pgup", label: "PgUp/PgDn", action: inputActionGuardPage, delta: -1},
		{key: "pgdown", label: "PgUp/PgDn", action: inputActionGuardPage, delta: 1},
		{key: "home", label: "Home/End", action: inputActionGuardBoundary, delta: -1},
		{key: "end", label: "Home/End", action: inputActionGuardBoundary, delta: 1},
	} {
		binding := actionBinding(direction.action, direction.label, "review", direction.key)
		binding.argument = direction.delta
		bindings = append(bindings, binding)
	}
	return bindings
}
