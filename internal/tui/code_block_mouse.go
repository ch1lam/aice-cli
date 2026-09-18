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
	// -2 is the fold heading, -1 the Copy button; otherwise a source row.
	sourceLine int
	text       string
	expansion  *bool
	expanded   bool
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
	var expansion *bool
	if code.row == 0 {
		if layout.block.expanded != nil && x >= 2 && x < codeFoldEnd(layout) {
			expansion, sourceLine, text = layout.block.expanded, -2, ""
		} else if layout.copyColumn == 0 || x < layout.copyColumn || x >= layout.width-2 {
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
		sourceLine: sourceLine, text: text, expansion: expansion, expanded: layout.expanded}
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
	if layout.block.source != hover.source || (hover.expansion == nil && layout.rows[code.row].sourceLine != hover.sourceLine) {
		return row.text
	}
	start, end := block.column+2, block.column+layout.width-2
	if hover.sourceLine < 0 {
		if code.row != 0 {
			return row.text
		}
		if hover.expansion != nil {
			end = block.column + codeFoldEnd(layout)
		} else {
			start = block.column + layout.copyColumn
		}
	}
	return ansi.Cut(row.text, 0, start) + paintCodeBackground(ansi.Cut(row.text, start, end), subtleColor) +
		ansi.Cut(row.text, end, ansi.StringWidth(row.text))
}

func codeFoldEnd(layout codeBlockLayout) int {
	if layout.copyColumn > 0 {
		return layout.copyColumn - 1
	}
	return layout.width - 2
}

// The clicked code heading stays anchored while only its containing Markdown
// group is invalidated. Literal source and neighboring groups remain intact.
func (m *model) toggleCode(hit codeHit) {
	if hit.expansion == nil {
		return
	}
	for y, row := range m.viewport.visibleRows() {
		if row.key != hit.key || row.part != hit.part || row.code.placement == nil || row.code.block != hit.block {
			continue
		}
		*hit.expansion = !*hit.expansion
		part := &m.viewport.itemParts(row.item)[row.part]
		part.lines, part.codeRows = nil, nil
		m.viewport.index, m.viewport.part = row.item, row.part
		m.viewport.line = max(0, row.line-row.code.row)
		m.viewport.scroll(-max(0, y-row.code.row))
		return
	}
}

func (m *model) toggleVisibleCode() {
	for _, row := range m.viewport.visibleRows() {
		if p := row.code.placement; p != nil && p.layout.block.expanded != nil {
			m.toggleCode(codeHit{key: row.key, part: row.part, block: row.code.block, expansion: p.layout.block.expanded})
			return
		}
	}
}
