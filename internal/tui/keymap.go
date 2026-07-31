package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	send      key.Binding
	newline   key.Binding
	scroll    key.Binding
	process   key.Binding
	commands  key.Binding
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
		commands: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "commands"),
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

func (k keyMap) forState(running bool) keyMap {
	k.send.SetEnabled(!running)
	k.newline.SetEnabled(!running)
	k.quit.SetEnabled(!running)
	if running {
		k.interrupt.SetHelp("ctrl+C", "cancel")
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
		{k.send, k.newline},
		{k.commands, k.scroll, k.process, k.help},
		{k.interrupt, k.quit},
	}
}
