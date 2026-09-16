package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	send      key.Binding
	queue     key.Binding
	newline   key.Binding
	scroll    key.Binding
	process   key.Binding
	editor    key.Binding
	paste     key.Binding
	commands  key.Binding
	history   key.Binding
	help      key.Binding
	clear     key.Binding
	interrupt key.Binding
	quit      key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		paste: key.NewBinding(key.WithKeys("ctrl+v", "alt+v"), key.WithHelp("Ctrl+v/Alt+v", "paste image/text")),
		send: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("Enter", "send"),
		),
		queue: key.NewBinding(
			key.WithKeys("ctrl+enter"),
			key.WithHelp("Ctrl+Enter", "queue"),
		),
		newline: key.NewBinding(
			key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
			key.WithHelp("Shift+Enter", "newline"),
		),
		scroll: key.NewBinding(
			key.WithKeys("pgup", "pgdown"),
			key.WithHelp("PgUp/PgDn", "scroll"),
		),
		process: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("Ctrl+o", "process"),
		),
		editor: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("Ctrl+g", "editor"),
		),
		commands: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "commands"),
		),
		history: key.NewBinding(
			key.WithKeys("up", "down"),
			key.WithHelp("Up/Down", "history"),
		),
		help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "shortcuts"),
		),
		clear: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("Ctrl+c", "clear"),
		),
		interrupt: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("Esc", "cancel"),
		),
		quit: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("Ctrl+d", "quit"),
		),
	}
}

func (k keyMap) forState(running, acceptsDelivery bool) keyMap {
	composerEnabled := !running || acceptsDelivery
	k.send.SetEnabled(composerEnabled)
	k.newline.SetEnabled(composerEnabled)
	k.queue.SetEnabled(running && acceptsDelivery)
	k.history.SetEnabled(!running)
	k.quit.SetEnabled(!running)
	k.interrupt.SetEnabled(running)
	if running && acceptsDelivery {
		k.send.SetHelp("Enter", "steer")
	}
	return k
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.help,
		k.clear,
		k.interrupt,
	}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.send, k.queue, k.newline, k.paste},
		{k.commands, k.history, k.scroll, k.process, k.editor, k.help},
		{k.clear, k.interrupt, k.quit},
	}
}
