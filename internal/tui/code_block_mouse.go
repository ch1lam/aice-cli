package tui

import tea "charm.land/bubbletea/v2"

// A press remembers source and geometry, so streaming or reflow between press
// and release cannot copy text from a replacement block at the same position.
type codeHit struct {
	valid                          bool
	key, block, row, column, width int
	source                         string
}

func (m model) codeHitAt(mouse tea.Mouse) codeHit {
	if m.guardPending != nil || m.authInput != nil || m.secretInput != nil ||
		m.side.menu != nil || m.side.confirm != nil || m.commandMenu != nil {
		return codeHit{}
	}
	position, inside := m.transcriptMousePosition(mouse, false, 0)
	if !inside {
		return codeHit{}
	}
	rows := m.viewport.visibleRows()
	if position.row >= len(rows) {
		return codeHit{}
	}
	row := rows[position.row]
	code := row.code
	if code.placement == nil || code.row != 0 {
		return codeHit{}
	}
	block := code.placement
	x := position.column - block.column
	if block.layout.copyColumn == 0 || x < block.layout.copyColumn || x >= block.layout.width-2 {
		return codeHit{}
	}
	return codeHit{valid: true, key: row.key, block: code.block, row: row.line,
		column: block.column, width: block.layout.width, source: block.layout.block.source}
}
