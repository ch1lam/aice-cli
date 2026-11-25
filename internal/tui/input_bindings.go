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
	case inputQuestion:
		return m.questionInputBindings()
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
	case inputAgents:
		return m.agentInputBindings()
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
// Empty help labels keep basic navigation available without advertising it.
type inputHelpKeys []inputBinding

func (bindings inputHelpKeys) help(full bool) []key.Binding {
	var result []key.Binding
	for _, item := range bindings.visible(full) {
		result = append(result, item.binding)
	}
	return result
}

// visible lists the advertised bindings once each, in order.
func (bindings inputHelpKeys) visible(full bool) []inputBinding {
	var result []inputBinding
	seen := make(map[key.Help]bool)
	for _, item := range bindings {
		h := item.binding.Help()
		if !item.binding.Enabled() || !full && !item.short || h.Key == "" || seen[h] {
			continue
		}
		seen[h] = true
		result = append(result, item)
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

// hintSegment is where one advertised binding shows in a hint line, in
// cells from the line's start; a click there runs the binding's action.
type hintSegment struct {
	item     inputBinding
	x, width int
}

func hintAt(segments []hintSegment, x int) (inputBinding, bool) {
	for _, segment := range segments {
		if x >= segment.x && x < segment.x+segment.width {
			return segment.item, true
		}
	}
	return inputBinding{}, false
}

func inputBindingsHelp(bindings []inputBinding, width int, full bool) string {
	text, _ := inputBindingsHints(bindings, width, full)
	return text
}

const hintSeparator = " · "

// inputBindingsHints lays out the advertised bindings as "Key action"
// parts joined by hintSeparator within width, and reports where each went.
func inputBindingsHints(bindings []inputBinding, width int, full bool) (string, []hintSegment) {
	items := inputHelpKeys(bindings).visible(full)
	if width <= 0 || len(items) == 0 {
		return "", nil
	}
	long := func(item inputBinding) string { h := item.binding.Help(); return h.Key + " " + h.Desc }
	short := func(item inputBinding) string { return item.binding.Help().Key }
	joined := func(items []inputBinding, label func(inputBinding) string) int {
		total := 0
		for i, item := range items {
			total += ansi.StringWidth(label(item))
			if i > 0 {
				total += ansi.StringWidth(hintSeparator)
			}
		}
		return total
	}
	var (
		text     string
		segments []hintSegment
	)
	add := func(item inputBinding, label string) bool {
		x := ansi.StringWidth(text)
		if text != "" {
			x += ansi.StringWidth(hintSeparator)
		}
		if x+ansi.StringWidth(label) > width {
			return false
		}
		if text != "" {
			text += hintSeparator
		}
		segments = append(segments, hintSegment{item: item, x: x, width: ansi.StringWidth(label)})
		text += label
		return true
	}
	if joined(items, long) <= width {
		for _, item := range items {
			add(item, long(item))
		}
		return text, segments
	}
	// The first two actions are the local confirm/back controls. Keep both
	// discoverable before spending width on lower-priority navigation or aliases.
	count := min(2, len(items))
	first := long
	if joined(items[:count], long) > width {
		first = short
	}
	if joined(items[:count], first) > width {
		plain := short(items[0])
		if count > 1 {
			plain += hintSeparator + short(items[1])
		}
		return ansi.Truncate(plain, width, "…"), nil
	}
	for _, item := range items[:count] {
		add(item, first(item))
	}
	for _, item := range items[count:] {
		if !add(item, long(item)) {
			add(item, short(item))
		}
	}
	return text, segments
}

// styleHints renders a hint line in the muted style, underlining the part
// at hover (a cell offset, or -1) to show that it is clickable.
func styleHints(text string, segments []hintSegment, hover int) string {
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		if hover < segment.x || hover >= segment.x+segment.width {
			continue
		}
		end := segment.x + segment.width
		return mutedStyle.Render(ansi.Cut(text, 0, segment.x)) +
			mutedStyle.Underline(true).Render(ansi.Cut(text, segment.x, end)) +
			mutedStyle.Render(ansi.Cut(text, end, ansi.StringWidth(text)))
	}
	return mutedStyle.Render(text)
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
		{action: inputActionEditor, binding: k.editor},
		{action: inputActionClear, binding: k.clear, short: true},
		{action: inputActionClose, binding: k.close, short: side},
		{action: inputActionQuit, binding: k.quit, short: side},
		{action: inputActionMode, binding: k.mode, short: true},
	}
	if k.help.Enabled() || m.deliveryPending {
		help := inputBinding{action: inputActionHelp, binding: k.help, short: true}
		if side || m.run.running() {
			bindings = append(bindings, help)
		} else {
			bindings = append([]inputBinding{help}, bindings...)
		}
	}
	bindings = append(bindings,
		inputBinding{action: inputActionScroll, binding: k.scrollUp, argument: -1},
		inputBinding{action: inputActionScroll, binding: k.scrollDown, argument: 1},
	)
	// Down past the newest prompt continues into the subagent panel below
	// the composer; it is absent otherwise, so history and the editor keep Down.
	if !side && m.agentPanelReachable() {
		bindings = append(bindings, actionBinding(inputActionAgents, "↓", "subagents", "down"))
	}
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
		} else if m.clipboardRead.pending {
			switch bindings[i].action {
			case inputActionSend, inputActionQueue, inputActionPaste, inputActionEditor:
				bindings[i].blocked = inputActionClipboardPending
			}
		}
	}
	return bindings
}
