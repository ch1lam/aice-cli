package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m model) composerContains(mouse tea.Mouse, width int) bool {
	if m.guardPending != nil || !m.composerInputEnabled() || mouse.X < 0 || mouse.X >= width {
		return false
	}
	top := lipgloss.Height(m.headerView(width)) + m.viewport.Height() + lipgloss.Height(m.commandMenuView(width))
	height := lipgloss.Height(m.composerViewWithStyle(width, composerBlurredStyle))
	return mouse.Y >= top && mouse.Y < top+height
}

func (m model) composerHovered(width int) bool {
	return m.pointer.known && !m.selection.active &&
		m.composerContains(tea.Mouse{X: m.pointer.x, Y: m.pointer.y}, width)
}
