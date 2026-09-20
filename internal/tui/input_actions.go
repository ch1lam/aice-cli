package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

type inputAction string

const (
	inputActionSessions  inputAction = "sessions"
	inputActionTurns     inputAction = "turns"
	inputActionSend      inputAction = "send"
	inputActionQueue     inputAction = "queue"
	inputActionNewline   inputAction = "newline"
	inputActionScroll    inputAction = "scroll"
	inputActionProcess   inputAction = "process"
	inputActionCode      inputAction = "code"
	inputActionEditor    inputAction = "editor"
	inputActionPaste     inputAction = "paste"
	inputActionHistory   inputAction = "history"
	inputActionHelp      inputAction = "help"
	inputActionClear     inputAction = "clear"
	inputActionInterrupt inputAction = "interrupt"
	inputActionQuit      inputAction = "quit"
	inputActionClose     inputAction = "close"
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
	argument         int
	matched, enabled bool
	blocked          inputActionBlock
}

// inputActionKeys resolves conversation bindings from current state. The
// common action resolver adds local completion overrides before dispatch/help.
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
	history := main && !m.running && !m.slashCommandMenuVisible() && !m.fileCompletionVisible()
	k.historyUp.SetEnabled(history && m.historyBackAllowed())
	k.historyDown.SetEnabled(history && m.historyForwardAllowed())
	k.help.SetEnabled(conversation && empty)
	k.scrollUp.SetEnabled(conversation)
	k.scrollDown.SetEnabled(conversation)
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
		&k.sessions, &k.turns, &k.send, &k.queue, &k.newline, &k.scrollUp, &k.scrollDown,
		&k.process, &k.code, &k.editor, &k.paste, &k.commands, &k.historyUp, &k.historyDown,
		&k.help, &k.clear, &k.interrupt, &k.quit, &k.close,
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

// Expanded help metadata is small and comparable. It tracks every domain's
// resolved actions without rendering the transcript on background events.
func (m model) expandedHelpLayout() string {
	if !m.help.ShowAll {
		return ""
	}
	var layout strings.Builder
	for _, item := range m.inputBindings() {
		if item.binding.Enabled() {
			h := item.binding.Help()
			layout.WriteString(h.Key + "\x00" + h.Desc + "\x00")
		}
	}
	return layout.String()
}
