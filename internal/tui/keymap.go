package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	sessions    key.Binding
	turns       key.Binding
	send        key.Binding
	queue       key.Binding
	newline     key.Binding
	scrollUp    key.Binding
	scrollDown  key.Binding
	process     key.Binding
	code        key.Binding
	editor      key.Binding
	paste       key.Binding
	commands    key.Binding
	historyUp   key.Binding
	historyDown key.Binding
	help        key.Binding
	clear       key.Binding
	interrupt   key.Binding
	quit        key.Binding
	close       key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		close:    key.NewBinding(key.WithKeys("alt+esc"), key.WithHelp("Alt+Esc", "close")),
		code:     key.NewBinding(key.WithKeys("alt+o"), key.WithHelp("Alt+o", "visible code")),
		turns:    key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("Ctrl+t", "questions")),
		sessions: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("Ctrl+r", "history")),
		paste:    key.NewBinding(key.WithKeys("ctrl+v", "alt+v")),
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
		scrollUp: key.NewBinding(
			key.WithKeys("pgup"),
		),
		scrollDown: key.NewBinding(
			key.WithKeys("pgdown"),
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
		historyUp: key.NewBinding(
			key.WithKeys("up"),
		),
		historyDown: key.NewBinding(
			key.WithKeys("down"),
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
