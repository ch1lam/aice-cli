package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var transcriptHoverStyle = lipgloss.NewStyle().
	Foreground(primaryTextColor).Background(panelBlackColor)

type transcriptPointer struct {
	x, y  int
	known bool
}

type foldHit struct {
	target               foldTarget
	key, line, screenRow int
}

func (m model) foldHitAt(mouse tea.Mouse) foldHit {
	blocked := m.side.isVisible || m.guardPending != nil || m.authInput != nil ||
		m.secretInput != nil || m.side.menu != nil || m.side.confirm != nil || m.commandMenu != nil
	if blocked || mouse.X < 0 || mouse.X >= m.viewport.Width() {
		return foldHit{}
	}
	y := mouse.Y - lipgloss.Height(m.headerView(max(m.width, minimumWidth)))
	if y < 0 || y >= m.viewport.Height() {
		return foldHit{}
	}
	rows := m.viewport.visibleRows()
	if y >= len(rows) || rows[y].fold.kind == foldNone {
		return foldHit{}
	}
	row := rows[y]
	return foldHit{target: row.fold, key: row.key, line: row.line, screenRow: y}
}

func (m model) hoveredFold() foldTarget {
	if !m.pointer.known || m.selection.active {
		return foldTarget{}
	}
	return m.foldHitAt(tea.Mouse{X: m.pointer.x, Y: m.pointer.y}).target
}

func (m *model) trackPointer(mouse tea.Mouse) {
	m.pointer = transcriptPointer{x: mouse.X, y: mouse.Y, known: true}
}

func (m *model) toggleFoldAt(hit foldHit) {
	m.setFoldExpanded(hit.target, !m.foldExpanded(hit.target))
	// Bypass automatic bottom following: the clicked header is the reading
	// anchor, even if the transcript fitted on screen before the expansion.
	m.viewport.setItems(m.transcriptItems())
	m.viewport.anchorRow(hit.key, hit.line, hit.screenRow)
}
