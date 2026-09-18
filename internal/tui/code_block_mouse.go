package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A press remembers source and geometry, so streaming or reflow between press
// and release cannot copy text from a replacement block at the same position.
type codeHit struct {
	valid                                bool
	key, part, block, row, column, width int
	source                               string
	// -1 is the whole-block button; otherwise an original logical source row.
	sourceLine int
	text       string
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
	if code.placement == nil {
		return codeHit{}
	}
	block := code.placement
	x := position.column - block.column
	layout := block.layout
	sourceLine := layout.rows[code.row].sourceLine
	text := layout.block.source
	if code.row == 0 {
		if layout.copyColumn == 0 || x < layout.copyColumn || x >= layout.width-2 {
			return codeHit{}
		}
	} else {
		if sourceLine < 0 || x < 2 || x >= layout.width-2 {
			return codeHit{}
		}
		text = layout.block.lines[sourceLine]
		if line, terminated := strings.CutSuffix(text, "\n"); terminated {
			text = strings.TrimSuffix(line, "\r")
		}
	}
	return codeHit{valid: true, key: row.key, part: row.part, block: code.block, row: row.line,
		column: block.column, width: layout.width, source: layout.block.source,
		sourceLine: sourceLine, text: text}
}

func (m model) hoveredCode() codeHit {
	if !m.pointer.known || m.selection.active {
		return codeHit{}
	}
	return m.codeHitAt(tea.Mouse{X: m.pointer.x, Y: m.pointer.y})
}

func (row transcriptRow) withCodeHover(hover codeHit) string {
	code := row.code
	if !hover.valid || row.key != hover.key || row.part != hover.part || code.placement == nil || code.block != hover.block {
		return row.text
	}
	block := code.placement
	layout := block.layout
	if layout.block.source != hover.source || layout.rows[code.row].sourceLine != hover.sourceLine {
		return row.text
	}
	start, end := block.column+2, block.column+layout.width-2
	if hover.sourceLine < 0 {
		if code.row != 0 {
			return row.text
		}
		start = block.column + layout.copyColumn
	}
	return ansi.Cut(row.text, 0, start) + paintCodeBackground(ansi.Cut(row.text, start, end), subtleColor) +
		ansi.Cut(row.text, end, ansi.StringWidth(row.text))
}
