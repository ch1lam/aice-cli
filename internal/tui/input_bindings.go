package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// inputBinding is a resolved action, not another owner of UI state. A disabled
// binding reserves its keys; an absent binding leaves them to the active editor.
type inputBinding struct {
	action   inputAction
	binding  key.Binding
	argument int
	blocked  inputActionBlock
	short    bool
}

func actionBinding(action inputAction, label, description string, keys ...string) inputBinding {
	return inputBinding{action: action, binding: key.NewBinding(key.WithKeys(keys...), key.WithHelp(label, description)), short: true}
}

func (m model) inputBindings() []inputBinding {
	switch m.inputContext().domain {
	case inputReading:
		return m.readingInputBindings()
	case inputSessions:
		return m.sessionInputBindings()
	case inputGuard:
		return m.guardInputBindings()
	case inputAuth:
		return m.authInputBindings()
	case inputSideMenu:
		return m.sideMenuInputBindings()
	case inputSideConfirm:
		return m.sideConfirmInputBindings()
	case inputSecret:
		return m.secretInputBindings()
	case inputCommand:
		return m.commandInputBindings()
	default:
		bindings := m.conversationInputBindings()
		if m.inputContext().domain == inputMain && !m.deliveryPending {
			// Completion owns only its declared keys. Remove shadowed base bindings
			// before both matching and help, including while completion is pending.
			bindings = overrideInputBindings(bindings, m.completionInputBindings())
		}
		return bindings
	}
}

func overrideInputBindings(base, overrides []inputBinding) []inputBinding {
	if len(overrides) == 0 {
		return base
	}
	reserved := make(map[string]bool)
	for _, item := range overrides {
		for _, k := range item.binding.Keys() {
			reserved[k] = true
		}
	}
	result := append([]inputBinding(nil), overrides...)
	for _, item := range base {
		var keys []string
		for _, k := range item.binding.Keys() {
			if !reserved[k] {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			item.binding.SetKeys(keys...)
			result = append(result, item)
		}
	}
	return result
}

func (m model) matchInputAction(message tea.KeyPressMsg) inputActionMatch {
	return matchInputBindings(m.inputBindings(), message)
}

func matchInputBindings(bindings []inputBinding, message tea.KeyPressMsg) inputActionMatch {
	for _, item := range bindings {
		raw := item.binding
		raw.SetEnabled(true)
		if !key.Matches(message, raw) {
			continue
		}
		blocked := item.blocked
		if !item.binding.Enabled() && blocked == inputActionAvailable {
			blocked = inputActionUnavailable
		}
		return inputActionMatch{action: item.action, argument: item.argument, matched: true, enabled: item.binding.Enabled(), blocked: blocked}
	}
	return inputActionMatch{}
}

// Help consumes the same effective bindings as dispatch. Repeated labels group
// directional actions without introducing a second set of key definitions.
type inputHelpKeys []inputBinding

func (bindings inputHelpKeys) help(full bool) []key.Binding {
	var result []key.Binding
	seen := make(map[key.Help]bool)
	for _, item := range bindings {
		h := item.binding.Help()
		if !item.binding.Enabled() || !full && !item.short || h.Key == "" || seen[h] {
			continue
		}
		seen[h] = true
		result = append(result, item.binding)
	}
	return result
}

func (bindings inputHelpKeys) ShortHelp() []key.Binding { return bindings.help(false) }
func (bindings inputHelpKeys) FullHelp() [][]key.Binding {
	all := bindings.help(true)
	var rows [][]key.Binding
	for len(all) > 0 {
		size := min(5, len(all))
		rows = append(rows, all[:size])
		all = all[size:]
	}
	return rows
}

func (m model) footerKeys() inputHelpKeys { return m.inputBindings() }

func (m model) inputHelp(width int, full bool) string {
	return inputBindingsHelp(m.inputBindings(), width, full)
}

func inputBindingsHelp(bindings []inputBinding, width int, full bool) string {
	keys := inputHelpKeys(bindings).help(full)
	if width <= 0 || len(keys) == 0 {
		return ""
	}
	parts := make([]string, len(keys))
	for i, binding := range keys {
		h := binding.Help()
		parts[i] = h.Key + " " + h.Desc
	}
	if text := strings.Join(parts, " · "); ansi.StringWidth(text) <= width {
		return text
	}
	// The first two actions are the local confirm/back controls. Keep both
	// discoverable before spending width on lower-priority navigation or aliases.
	count := min(2, len(parts))
	result := strings.Join(parts[:count], " · ")
	if ansi.StringWidth(result) > width {
		for i := range count {
			parts[i] = keys[i].Help().Key
		}
		result = strings.Join(parts[:count], " · ")
	}
	if ansi.StringWidth(result) > width {
		return ansi.Truncate(result, width, "…")
	}
	for i := count; i < len(parts); i++ {
		next := result + " · " + parts[i]
		if ansi.StringWidth(next) > width {
			next = result + " · " + keys[i].Help().Key
		}
		if ansi.StringWidth(next) <= width {
			result = next
		}
	}
	return result
}

// Full help wraps complete actions rather than letting column layout silently
// omit later actions on a narrow terminal.
func (m model) inputFullHelp(width int) string {
	width = max(width, 1)
	var rows []string
	line := ""
	for _, binding := range inputHelpKeys(m.inputBindings()).help(true) {
		h := binding.Help()
		item := m.help.Styles.FullKey.Render(h.Key) + " " + m.help.Styles.FullDesc.Render(h.Desc)
		next := item
		if line != "" {
			next = line + m.help.Styles.FullSeparator.Render(" · ") + item
		}
		if ansi.StringWidth(next) <= width {
			line = next
			continue
		}
		if line != "" {
			rows = append(rows, line)
		}
		if ansi.StringWidth(item) > width {
			rows = append(rows, ansi.Hardwrap(item, width, true))
			line = ""
		} else {
			line = item
		}
	}
	if line != "" {
		rows = append(rows, line)
	}
	return strings.Join(rows, "\n")
}

func (m model) conversationInputBindings() []inputBinding {
	k := m.inputActionKeys()
	if m.clearQuitPending && !m.deliveryPending {
		k.clear.SetHelp("Ctrl+c", "quit")
	}
	if m.help.ShowAll {
		k.help.SetHelp("?", "close")
		if !m.clearQuitPending && !m.deliveryPending {
			k.clear.SetHelp("Ctrl+c", "clear, then quit")
		}
	}
	side := m.inputContext().domain == inputSide
	bindings := []inputBinding{
		{action: inputActionSend, binding: k.send, short: side},
		{action: inputActionInterrupt, binding: k.interrupt, short: true},
		{action: inputActionQueue, binding: k.queue},
		{action: inputActionNewline, binding: k.newline, short: side},
		{action: inputActionPaste, binding: k.paste},
		{action: inputActionSessions, binding: k.sessions},
		{action: inputActionTurns, binding: k.turns},
		{action: inputActionProcess, binding: k.process},
		{action: inputActionCode, binding: k.code},
		{action: inputActionEditor, binding: k.editor},
		{action: inputActionClear, binding: k.clear, short: true},
		{action: inputActionClose, binding: k.close, short: side},
		{action: inputActionQuit, binding: k.quit, short: side},
	}
	if k.help.Enabled() || m.deliveryPending {
		help := inputBinding{action: inputActionHelp, binding: k.help, short: true}
		if side || m.running {
			bindings = append(bindings, help)
		} else {
			bindings = append([]inputBinding{help}, bindings...)
		}
	}
	bindings = append(bindings,
		inputBinding{action: inputActionScroll, binding: k.scrollUp, argument: -1},
		inputBinding{action: inputActionScroll, binding: k.scrollDown, argument: 1},
	)
	if k.historyUp.Enabled() {
		bindings = append(bindings, inputBinding{action: inputActionHistory, binding: k.historyUp, argument: -1})
	}
	if k.historyDown.Enabled() {
		bindings = append(bindings, inputBinding{action: inputActionHistory, binding: k.historyDown, argument: 1})
	}
	// Slash remains ordinary composer text, with a discoverability hint supplied
	// by its actual insertion action only at the start of an empty draft.
	if k.commands.Enabled() && m.input.Value() == "" {
		bindings = append(bindings, inputBinding{action: inputActionCommands, binding: k.commands})
	}
	for i := range bindings {
		if bindings[i].binding.Enabled() {
			continue
		}
		bindings[i].blocked = inputActionUnavailable
		if m.deliveryPending {
			bindings[i].blocked = inputActionDeliveryPending
		} else if m.clipboardPending {
			switch bindings[i].action {
			case inputActionSend, inputActionQueue, inputActionPaste, inputActionEditor:
				bindings[i].blocked = inputActionClipboardPending
			}
		}
	}
	return bindings
}
