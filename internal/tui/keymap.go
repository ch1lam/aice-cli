package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	send      key.Binding
	queue     key.Binding
	newline   key.Binding
	scroll    key.Binding
	process   key.Binding
	editor    key.Binding
	commands  key.Binding
	history   key.Binding
	help      key.Binding
	interrupt key.Binding
	quit      key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		send: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send"),
		),
		queue: key.NewBinding(
			key.WithKeys("ctrl+enter"),
			key.WithHelp("ctrl+enter", "queue"),
		),
		newline: key.NewBinding(
			key.WithKeys("shift+enter", "alt+enter", "ctrl+j"),
			key.WithHelp("shift+enter", "newline"),
		),
		scroll: key.NewBinding(
			key.WithKeys("pgup", "pgdown"),
			key.WithHelp("pgup/pgdn", "scroll"),
		),
		process: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "process"),
		),
		editor: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("ctrl+g", "editor"),
		),
		commands: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "commands"),
		),
		history: key.NewBinding(
			key.WithKeys("up", "down"),
			key.WithHelp("up/down", "history"),
		),
		help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "shortcuts"),
		),
		interrupt: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+C", "quit"),
		),
		quit: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "quit"),
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
	if running {
		k.interrupt.SetHelp("ctrl+C", "cancel")
	}
	if running && acceptsDelivery {
		k.send.SetHelp("enter", "steer")
	}
	return k
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.help,
		k.interrupt,
	}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.send, k.queue, k.newline},
		{k.commands, k.history, k.scroll, k.process, k.editor, k.help},
		{k.interrupt, k.quit},
	}
}
