package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m model) composerContains(mouse tea.Mouse, _ int) bool {
	return m.composerInputEnabled() && m.screenLayout().composer.contains(mouse)
}

func (m model) composerHovered(width int) bool {
	return m.pointer.known && !m.selection.active &&
		m.composerContains(tea.Mouse{X: m.pointer.x, Y: m.pointer.y}, width)
}
