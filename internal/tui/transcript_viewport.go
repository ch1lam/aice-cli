package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// transcriptItem delays formatting until scrolling reaches this block. Version
// must be comparable and describe every input to render other than width.
type transcriptItem struct {
	key           int
	fold          foldTarget
	version       any
	render        func() string
	renderContent func() transcriptContent
	// Split immutable Markdown only when this item is reached.
	split       func() []transcriptItem
	parts       []transcriptItem
	source      string
	revealMatch func(string) bool
	// Optional styled header variant; it must have identical text and wrapping.
	renderHover func() string
	hoverLines  []string
	gap         int
	lines       []string
	width       int
	codeRows    map[int]transcriptCodeRow
}

func (item transcriptItem) content() transcriptContent {
	if item.renderContent != nil {
		return item.renderContent()
	}
	return transcriptContent{view: item.render()}
}

type transcriptCodeRow struct {
	placement  *codeBlockPlacement
	block, row int
}

// transcriptViewport anchors scrolling to an item and a row inside it. It does
// not need to render preceding history to locate the visible window. All state,
// including cached wrapped lines, belongs to the TUI update/render loop.
type transcriptViewport struct {
	items                  []transcriptItem
	width, height          int
	index, part, line      int
	MouseWheelDelta        int
	itemsWidth             int
	resizing, resizeFollow bool
}

func newTranscriptViewport() transcriptViewport {
	return transcriptViewport{width: 80, height: 20, MouseWheelDelta: 3}
}

func (v *transcriptViewport) setItems(items []transcriptItem) {
	anchor, hasAnchor := 0, len(v.items) > 0 && v.index < len(v.items)
	if hasAnchor {
		anchor = v.items[v.index].key
	}
	nextIndex := min(v.index, max(len(items)-1, 0))
	anchorFound := false
	for i := range items {
		if hasAnchor && items[i].key == anchor {
			nextIndex = i
			anchorFound = true
		}
		if i < len(v.items) && v.itemsWidth == v.width {
			old := v.items[i]
			if old.key == items[i].key && old.version == items[i].version {
				items[i].lines, items[i].width = old.lines, old.width
				items[i].codeRows = old.codeRows
				items[i].hoverLines = old.hoverLines
				items[i].parts = old.parts
			}
		}
	}
	v.items, v.itemsWidth, v.index, v.resizing = items, v.width, nextIndex, false
	if !anchorFound {
		v.part, v.line = 0, 0
	}
	if len(items) > 0 && v.part > 0 {
		v.part = min(v.part, len(v.itemParts(v.index))-1)
	}
	if len(items) > 0 && v.line > 0 {
		v.line = min(v.line, v.partHeight(v.index, v.part)-1)
	}
}

func (v transcriptViewport) Width() int  { return v.width }
func (v transcriptViewport) Height() int { return v.height }
func (v *transcriptViewport) SetWidth(width int) {
	width = max(width, 1)
	if width != v.width {
		v.beginResize()
		v.width = width
	}
}
func (v *transcriptViewport) SetHeight(height int) {
	height = max(height, 1)
	if height != v.height {
		v.beginResize()
		v.height = height
	}
}
func (v *transcriptViewport) beginResize() {
	if !v.resizing {
		v.resizeFollow = v.AtBottom()
		v.resizing = true
	}
}

func (v transcriptViewport) itemParts(index int) []transcriptItem {
	item := &v.items[index]
	if item.split == nil {
		return v.items[index : index+1]
	}
	if item.parts == nil {
		item.parts = item.split()
		if len(item.parts) == 0 {
			item.parts = []transcriptItem{staticTranscriptItem(item.key, "")}
		}
		item.parts[0].gap += item.gap
	}
	return item.parts
}

func (v transcriptViewport) partLines(index, part int) []string {
	item := &v.itemParts(index)[part]
	if item.lines == nil || item.width != v.width {
		item.hoverLines = nil
		item.lines, item.codeRows = wrapTranscriptContent(item.content(), v.width)
		item.width = v.width
	}
	return item.lines
}

func (v transcriptViewport) partHeight(index, part int) int {
	return len(v.partLines(index, part)) + v.itemParts(index)[part].gap
}

func (v transcriptViewport) AtBottom() bool {
	if v.resizing {
		return v.resizeFollow
	}
	remaining := -v.line
	for i := v.index; i < len(v.items); i++ {
		start := 0
		if i == v.index {
			start = v.part
		}
		for part := start; part < len(v.itemParts(i)); part++ {
			remaining += v.partHeight(i, part)
			if remaining > v.height {
				return false
			}
		}
	}
	return true
}

func (v *transcriptViewport) GotoTop() { v.index, v.part, v.line = 0, 0, 0 }
func (v *transcriptViewport) GotoBottom() {
	remaining := v.height
	v.GotoTop()
	for i := len(v.items) - 1; i >= 0; i-- {
		for part := len(v.itemParts(i)) - 1; part >= 0; part-- {
			height := v.partHeight(i, part)
			if height >= remaining {
				v.index, v.part, v.line = i, part, height-remaining
				return
			}
			remaining -= height
		}
	}
}

// YOffset is a selection coordinate, not an exact scrollbar total. Unknown
// blocks count as one row; measuring them here would defeat lazy rendering.
// Drag selection freezes both this coordinate and the visible rows.
func (v transcriptViewport) YOffset() int {
	offset := v.line
	for i := 0; i <= v.index && i < len(v.items); i++ {
		item := v.items[i]
		if item.parts == nil {
			if i < v.index {
				offset += max(1, len(item.lines)) + item.gap
			}
			continue
		}
		for part, piece := range item.parts {
			if i == v.index && part >= v.part {
				break
			}
			offset += max(1, len(piece.lines)) + piece.gap
		}
	}
	return offset
}

func (v *transcriptViewport) SetYOffset(offset int) {
	v.GotoTop()
	v.scroll(max(offset, 0))
}

func (v *transcriptViewport) previousPart() bool {
	if v.part > 0 {
		v.part--
		return true
	}
	if v.index == 0 {
		return false
	}
	v.index--
	v.part = len(v.itemParts(v.index)) - 1
	return true
}

func (v *transcriptViewport) nextPart() bool {
	if v.part+1 < len(v.itemParts(v.index)) {
		v.part++
		return true
	}
	if v.index+1 >= len(v.items) {
		return false
	}
	v.index++
	v.part = 0
	return true
}

func (v *transcriptViewport) scroll(delta int) {
	if len(v.items) == 0 {
		return
	}
	if delta < 0 {
		for delta < 0 {
			take := min(-delta, v.line)
			v.line -= take
			delta += take
			if delta == 0 || !v.previousPart() {
				break
			}
			v.line = v.partHeight(v.index, v.part)
		}
	} else {
		for delta > 0 {
			remaining := v.partHeight(v.index, v.part) - v.line
			if delta < remaining {
				v.line += delta
				break
			}
			if !v.nextPart() {
				v.GotoBottom()
				return
			}
			delta -= remaining
			v.line = 0
		}
		if v.AtBottom() {
			v.GotoBottom()
		}
	}
}

func (v *transcriptViewport) PageUp()   { v.scroll(-v.height) }
func (v *transcriptViewport) PageDown() { v.scroll(v.height) }

func (v transcriptViewport) Update(msg tea.Msg) (transcriptViewport, tea.Cmd) {
	if mouse, ok := msg.(tea.MouseWheelMsg); ok {
		switch mouse.Button {
		case tea.MouseWheelUp:
			v.scroll(-v.MouseWheelDelta)
		case tea.MouseWheelDown:
			v.scroll(v.MouseWheelDelta)
		}
	}
	return v, nil
}

func (v transcriptViewport) View() string {
	return v.viewWithHover(foldTarget{})
}

// Rendering and hit-testing walk exactly the same wrapped, clipped rows.
// A row carries the item-local position needed to anchor a fold without
// measuring all preceding history (YOffset deliberately isn't that measure).
type transcriptRow struct {
	text string
	item int
	part int
	key  int
	line int
	fold foldTarget
	code transcriptCodeRow
}

func (v transcriptViewport) visibleRows() []transcriptRow {
	rows := make([]transcriptRow, 0, v.height)
	skip := v.line
	for i := v.index; i < len(v.items) && len(rows) < v.height; i++ {
		start := 0
		if i == v.index {
			start = v.part
		}
		for part := start; part < len(v.itemParts(i)) && len(rows) < v.height; part++ {
			lines := v.partLines(i, part)
			item := v.itemParts(i)[part]
			height := item.gap + len(lines)
			if skip >= height {
				skip -= height
				continue
			}
			for line := skip; line < height && len(rows) < v.height; line++ {
				row := transcriptRow{item: i, part: part, key: v.items[i].key, line: line}
				if line >= item.gap {
					row.text, row.fold = lines[line-item.gap], item.fold
					row.code = item.codeRows[line-item.gap]
				}
				rows = append(rows, row)
			}
			skip = 0
		}
	}
	return rows
}

func (v transcriptViewport) viewWithHover(hover foldTarget) string {
	return v.viewWithCodeHover(hover, codeHit{})
}

func (v transcriptViewport) viewWithCodeHover(hover foldTarget, codeHover codeHit) string {
	var hoverLines []string
	hoverGap := 0
	lines := make([]string, 0, v.height)
	for _, row := range v.visibleRows() {
		text := row.withCodeHover(codeHover)
		if hover.kind != foldNone && row.fold == hover {
			if item := &v.itemParts(row.item)[row.part]; hoverLines == nil && item.renderHover != nil {
				if item.hoverLines == nil {
					item.hoverLines = wrapTranscriptLines(item.renderHover(), v.width)
				}
				hoverLines, hoverGap = item.hoverLines, item.gap
			}
			text = transcriptHoverStyle.Render(ansi.Strip(text))
			if line := row.line - hoverGap; line >= 0 && line < len(hoverLines) {
				text = hoverLines[line]
			}
		}
		lines = append(lines, text)
	}
	return lipgloss.NewStyle().Width(v.width).Height(v.height).Render(strings.Join(lines, "\n"))
}

func (v *transcriptViewport) anchorRow(key, line, screenRow int) {
	for index, item := range v.items {
		if item.key == key {
			v.index, v.part, v.line = index, 0, min(line, v.partHeight(index, 0)-1)
			v.scroll(-screenRow)
			return
		}
	}
}

// Full-content access is for explicit snapshots/tests, never a frame or scroll.
func (v transcriptViewport) GetContent() string {
	var lines []string
	for i := range v.items {
		for part, item := range v.itemParts(i) {
			for range item.gap {
				lines = append(lines, "")
			}
			lines = append(lines, v.partLines(i, part)...)
		}
	}
	return strings.Join(lines, "\n")
}

func (v *transcriptViewport) SetContent(content string) {
	v.setItems([]transcriptItem{{version: content, render: func() string { return content }}})
	if v.AtBottom() {
		v.GotoBottom()
	}
}

// Each wrapped row must carry its own styling: the viewport may start midway
// through a colored line. Hardwrap chooses grapheme-safe boundaries; Cut
// preserves the ANSI state when extracting each independently visible row.
func wrapTranscriptLines(content string, width int) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		wrapped := strings.Split(ansi.Hardwrap(line, width, true), "\n")
		if len(wrapped) == 1 {
			lines = append(lines, line)
			continue
		}
		offset := 0
		for _, row := range wrapped {
			end := offset + ansi.StringWidth(row)
			lines = append(lines, ansi.Cut(line, offset, end))
			offset = end
		}
	}
	return lines
}

func wrapTranscriptContent(content transcriptContent, width int) ([]string, map[int]transcriptCodeRow) {
	if len(content.blocks) == 0 {
		return wrapTranscriptLines(content.view, width), nil
	}
	var lines []string
	var offsets []int
	for row := range strings.SplitSeq(content.view, "\n") {
		offsets = append(offsets, len(lines))
		lines = append(lines, wrapTranscriptLines(row, width)...)
	}
	codeRows := make(map[int]transcriptCodeRow)
	for i := range content.blocks {
		block := &content.blocks[i]
		// Very small terminal clipping must never leave an invisible target.
		if block.column < 0 || block.column+block.layout.width > width {
			continue
		}
		for row := range block.layout.rows {
			codeRows[offsets[block.row+row]] = transcriptCodeRow{placement: block, block: i, row: row}
		}
	}
	return lines, codeRows
}
