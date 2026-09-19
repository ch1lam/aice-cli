package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type inputAction uint8

const (
	inputActionSessions inputAction = iota
	inputActionTurns
	inputActionSend
	inputActionQueue
	inputActionNewline
	inputActionScroll
	inputActionProcess
	inputActionCode
	inputActionEditor
	inputActionPaste
	inputActionHistory
	inputActionHelp
	inputActionClear
	inputActionInterrupt
	inputActionQuit
	inputActionClose
)

type inputActionBlock uint8

const (
	inputActionAvailable inputActionBlock = iota
	inputActionUnavailable
	inputActionClipboardPending
	inputActionDeliveryPending
)

type inputActionMatch struct {
	action           inputAction
	matched, enabled bool
	blocked          inputActionBlock
}

// inputActionKeys is the availability and label source for both dispatch and
// help. Local menus own their navigation; this projection only describes the
// main and side conversation domains.
func (m model) inputActionKeys() keyMap {
	k := m.keys
	c := m.inputContext()
	main, side := c.domain == inputMain, c.domain == inputSide
	conversation := main || side
	// Image attachments are represented by non-empty tokens in this value.
	empty := strings.TrimSpace(m.input.Value()) == ""
	k.send.SetEnabled(conversation && c.editor)
	if main && m.running && m.acceptsDelivery {
		k.send.SetHelp("Enter", "steer")
	}
	k.newline.SetEnabled(conversation && c.editor)
	k.queue.SetEnabled(main && m.running && m.acceptsDelivery)
	k.paste.SetEnabled(conversation && c.editor && m.clipboard != nil)
	k.editor.SetEnabled(conversation && c.editor && (side || !m.running))
	k.sessions.SetEnabled(main && !m.running && !m.side.anyRunning() && m.searchSessions != nil)
	k.turns.SetEnabled(main && !m.running && !m.side.anyRunning())
	k.commands.SetEnabled(main && !m.running && c.editor)
	k.history.SetEnabled(main && !m.running && !m.slashCommandMenuVisible() &&
		!m.fileCompletionVisible() && (m.historyBackAllowed() || m.historyForwardAllowed()))
	k.help.SetEnabled(conversation && empty)
	k.scroll.SetEnabled(conversation)
	k.process.SetEnabled(main)
	k.code.SetEnabled(main)
	k.clear.SetEnabled(conversation)
	k.interrupt.SetEnabled(main && (m.running || m.slashCommandMenuVisible() || m.fileCompletionVisible()))
	k.quit.SetEnabled(main && !m.running && empty)
	k.close.SetEnabled(side)
	if side {
		k.send.SetHelp("Enter", "ask")
		k.interrupt.SetEnabled(true)
		k.interrupt.SetHelp("Esc", "close")
		thread := m.side.activeThread()
		if thread != nil && !thread.isRunning && thread.readOnly() && m.isBTWCommandInput() {
			k.send.SetEnabled(true)
		}
		if m.side.newPending != nil || thread != nil && thread.isRunning {
			k.interrupt.SetHelp("Esc", "cancel")
		}
		k.quit.SetEnabled(thread != nil)
		k.quit.SetHelp("Ctrl+d", "end thread")
	}
	if conversation && m.isBTWCommandInput() {
		request, _ := parseSlashCommand(m.input.Value())
		label := "ask BTW"
		if strings.TrimSpace(request.Arguments) == "" {
			label = "BTW threads"
		}
		k.send.SetHelp("Enter", label)
		k.queue.SetHelp("Ctrl+Enter", label)
	}
	if m.clipboardPending {
		k.send.SetEnabled(false)
		k.queue.SetEnabled(false)
		k.paste.SetEnabled(false)
		k.editor.SetEnabled(false)
		k.sessions.SetEnabled(false)
		k.turns.SetEnabled(false)
	}
	if m.deliveryPending && conversation {
		for _, binding := range k.bindings() {
			binding.SetEnabled(false)
		}
		// Existing delivery cancellation accepts all three cancellation keys.
		k.interrupt.SetEnabled(true)
		k.clear.SetEnabled(true)
		k.quit.SetEnabled(true)
		k.close.SetEnabled(side)
		k.interrupt.SetHelp("Esc", "cancel delivery")
		k.clear.SetHelp("Ctrl+c", "cancel delivery")
		k.quit.SetHelp("Ctrl+d", "cancel delivery")
		k.close.SetHelp("Alt+Esc", "cancel delivery")
	}
	return k
}

func (k *keyMap) bindings() []*key.Binding {
	return []*key.Binding{
		&k.sessions, &k.turns, &k.send, &k.queue, &k.newline, &k.scroll,
		&k.process, &k.code, &k.editor, &k.paste, &k.commands, &k.history,
		&k.help, &k.clear, &k.interrupt, &k.quit, &k.close,
	}
}

func (m model) matchInputAction(message tea.KeyPressMsg) inputActionMatch {
	c := m.inputContext()
	if c.domain != inputMain && c.domain != inputSide {
		return inputActionMatch{}
	}
	for index, raw := range m.keys.actionBindings() {
		raw.SetEnabled(true)
		if !key.Matches(message, raw) {
			continue
		}
		action := inputAction(index)
		binding := m.inputActionKeys().actionBindings()[index]
		// Printable help and vertical cursor keys only become actions when
		// their conversation behavior applies. Otherwise the editor/menu owns them.
		if action == inputActionHelp && !binding.Enabled() && !m.deliveryPending {
			return inputActionMatch{}
		}
		if action == inputActionHistory && (!binding.Enabled() ||
			message.Code == tea.KeyUp && !m.historyBackAllowed() ||
			message.Code == tea.KeyDown && !m.historyForwardAllowed()) {
			return inputActionMatch{}
		}
		match := inputActionMatch{action: action, matched: true, enabled: binding.Enabled()}
		if !match.enabled {
			match.blocked = inputActionUnavailable
			if m.deliveryPending {
				match.blocked = inputActionDeliveryPending
			} else if m.clipboardPending && (action == inputActionSend ||
				action == inputActionQueue || action == inputActionPaste ||
				action == inputActionEditor) {
				match.blocked = inputActionClipboardPending
			}
		}
		return match
	}
	return inputActionMatch{}
}

func (k keyMap) actionBindings() [16]key.Binding {
	return [16]key.Binding{
		inputActionSessions: k.sessions, inputActionTurns: k.turns,
		inputActionSend: k.send, inputActionQueue: k.queue,
		inputActionNewline: k.newline, inputActionScroll: k.scroll,
		inputActionProcess: k.process, inputActionCode: k.code,
		inputActionEditor: k.editor, inputActionPaste: k.paste,
		inputActionHistory: k.history, inputActionHelp: k.help,
		inputActionClear: k.clear, inputActionInterrupt: k.interrupt,
		inputActionQuit: k.quit, inputActionClose: k.close,
	}
}

func (m model) blockedInputAction(match inputActionMatch) (model, tea.Cmd, bool) {
	if match.blocked == inputActionClipboardPending {
		m.inputNotice = "Reading clipboard; press Enter again when the attachment appears"
		if m.side.isVisible {
			m.side.notice = "Reading clipboard; press Enter again when ready"
		}
		return m.settleCommand(false, nil)
	}
	if match.blocked == inputActionUnavailable && match.action == inputActionSend && m.side.isVisible {
		if thread := m.side.activeThread(); thread != nil && thread.readOnly() && !thread.isRunning {
			m.side.notice = "This thread is read-only; /btw starts a new thread"
			m.refreshViewport(false)
		}
	}
	return m, nil, true
}

// Expanded help can reflow when an asynchronous state change enables actions.
// Comparing small binding metadata avoids rendering chrome on pointer events.
type helpLayoutEntry struct {
	enabled bool
	help    key.Help
}

func (m model) expandedHelpLayout() [17]helpLayoutEntry {
	var layout [17]helpLayoutEntry
	if !m.help.ShowAll || m.side.isVisible {
		return layout
	}
	keys := m.footerKeys()
	for i, binding := range keys.bindings() {
		if binding.Enabled() {
			layout[i] = helpLayoutEntry{true, binding.Help()}
		}
	}
	return layout
}
