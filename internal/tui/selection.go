package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type copyNoticeExpiredMsg uint64

type transcriptPosition struct {
	row    int
	column int
}

// selectionPoint is a content coordinate inside the frozen gesture version.
// item/part/line address the visual row (line includes the item gap, matching
// transcriptRow.line); column is a terminal display column using the same
// grapheme rules as the existing mouse mapping (not a UTF-8 byte index).
type selectionPoint struct {
	item   int
	part   int
	line   int
	column int
}

func (p selectionPoint) before(q selectionPoint) bool {
	if p.item != q.item {
		return p.item < q.item
	}
	if p.part != q.part {
		return p.part < q.part
	}
	if p.line != q.line {
		return p.line < q.line
	}
	return p.column <= q.column
}

func afterPoint(p selectionPoint) selectionPoint {
	p.column++
	return p
}

func rowBefore(a, b selectionPoint) bool {
	if a.item != b.item {
		return a.item < b.item
	}
	if a.part != b.part {
		return a.part < b.part
	}
	return a.line < b.line
}

func rowEqual(a, b selectionPoint) bool {
	return a.item == b.item && a.part == b.part && a.line == b.line
}

type transcriptSelection struct {
	anchor selectionPoint
	focus  selectionPoint
	// frozen is the content version fixed at press time. It shares the live
	// viewport's immutable layout caches but owns an independent scroll
	// anchor (index/part/line), so a wheel can browse the same version
	// without rebuilding deferred streaming items or copying all history.
	frozen  transcriptViewport
	active  bool
	moved   bool
	wheeled bool
	fold    foldHit
	code    codeHit
	// follow records whether the viewport was pinned to the bottom at press
	// time. Layout and following pause while the frozen snapshot owns the
	// screen; a drag release restores the pinned position with one catch-up
	// refresh instead of paying per-batch walks during the gesture.
	follow bool
	// highlight caches the current visible window's per-row geometry and
	// rendered output for the gesture, so each drag frame only restyles rows
	// in that window whose selected interval changed. Scrolling to a new
	// window rebuilds the cache for that window only.
	highlight *selectionHighlight
}

// selectionHighlight is the per-window highlight cache. The frozen content,
// its row widths and the code content intervals never change while the
// gesture is active; only the anchor/focus and the window move, so a frame
// only needs to restyle visible rows entering or leaving the selected range.
type selectionHighlight struct {
	winIndex int
	winPart  int
	winLine  int
	// original/rendered cover the frozen View for the window (height rows,
	// including lipgloss padding when the tail is short).
	original []string
	rendered []string
	widths   []int
	// codeStart/codeEnd bound the copyable source content per window row.
	// codeStart is negative for non-code rows, where the whole selected
	// interval is highlightable.
	codeStart []int
	codeEnd   []int
	// points carries the content identity per window row; padding rows use
	// item -1 and never match the selection.
	points   []selectionPoint
	selected bool
	lastStart selectionPoint
	lastEnd   selectionPoint
}

func (s *transcriptSelection) begin(
	anchor selectionPoint,
	frozen transcriptViewport,
) {
	s.anchor = anchor
	s.focus = anchor
	s.frozen = frozen
	s.active = true
	s.moved = false
	s.wheeled = false
	// A press reusing this struct must not inherit the previous gesture's
	// highlight cache: the frozen window is replaced below.
	s.highlight = nil
}

func (s *transcriptSelection) update(position selectionPoint) bool {
	if !s.active {
		return false
	}
	if position != s.anchor {
		s.moved = true
	}
	s.focus = position
	return true
}

func (s *transcriptSelection) finish() {
	s.active = false
}

func (s *transcriptSelection) clear() {
	*s = transcriptSelection{}
}

func (s transcriptSelection) selectedRange() (
	selectionPoint,
	selectionPoint,
	bool,
) {
	if !s.moved {
		return selectionPoint{}, selectionPoint{}, false
	}

	if s.anchor.before(s.focus) {
		return s.anchor, afterPoint(s.focus), true
	}
	return s.focus, afterPoint(s.anchor), true
}

// viewportPointAt maps a mouse position to a content coordinate in the given
// viewport version. With clamp it safely clamps to the viewport and to real
// content rows (bottom padding maps to the last row); without clamp it
// reports inside=false for outside coordinates.
func viewportPointAt(
	v *transcriptViewport,
	mouse tea.Mouse,
	m model,
	clamp bool,
) (selectionPoint, bool) {
	width := v.Width()
	height := v.Height()
	if width <= 0 || height <= 0 || len(v.items) == 0 {
		return selectionPoint{}, false
	}
	viewportTop := m.screenLayout().transcript.y
	x := mouse.X - m.horizontalPadding()
	y := mouse.Y - viewportTop
	inside := x >= 0 && x < width && y >= 0 && y < height
	if !inside && !clamp {
		return selectionPoint{}, false
	}
	x = min(max(x, 0), width-1)
	y = min(max(y, 0), height-1)
	rows := v.visibleRows()
	if len(rows) == 0 {
		return selectionPoint{}, false
	}
	if y >= len(rows) {
		y = len(rows) - 1
	}
	row := rows[y]
	return selectionPoint{item: row.item, part: row.part, line: row.line, column: x}, inside
}

func (m model) handleTranscriptMouseClick(
	message tea.MouseClickMsg,
) (model, tea.Cmd, bool) {
	if message.Button != tea.MouseLeft {
		return m, nil, false
	}

	anchor, inside := viewportPointAt(&m.viewport, message.Mouse(), m, false)
	if !inside {
		m.selection.clear()
		return m, nil, false
	}

	frozen := m.viewport
	m.selection.begin(anchor, frozen)
	// One walk per gesture, not per frame: items are warm in steady state.
	m.selection.follow = m.viewport.AtBottom()
	m.selection.fold = m.foldHitAt(message.Mouse())
	m.selection.code = m.codeHitAt(message.Mouse())
	return m, nil, true
}

func (m model) handleTranscriptMouseMotion(
	message tea.MouseMotionMsg,
) (model, tea.Cmd, bool) {
	if !m.selection.active {
		return m, nil, false
	}

	position, _ := viewportPointAt(&m.selection.frozen, message.Mouse(), m, true)
	if !m.selection.update(position) {
		m.selection.clear()
	}
	return m, nil, true
}

// handleSelectionWheel scrolls the frozen gesture version and immediately
// re-hits the focus with the current mouse position. Streaming layout stays
// deferred and live content never enters this version.
func (m *model) handleSelectionWheel(message tea.MouseWheelMsg) {
	if !m.selection.active {
		return
	}
	delta := m.selection.frozen.MouseWheelDelta
	switch message.Button {
	case tea.MouseWheelUp:
		delta = -delta
	case tea.MouseWheelDown:
	default:
		return
	}
	m.selection.frozen.scroll(delta)
	m.selection.wheeled = true
	if position, _ := viewportPointAt(&m.selection.frozen, message.Mouse(), *m, true); true {
		m.selection.update(position)
	}
}

func (m model) handleTranscriptMouseRelease(
	message tea.MouseReleaseMsg,
) (model, tea.Cmd, bool) {
	if !m.selection.active || message.Button != tea.MouseLeft {
		return m, nil, false
	}

	position, _ := viewportPointAt(&m.selection.frozen, message.Mouse(), m, true)
	if !m.selection.update(position) {
		m.selection.clear()
		return m, nil, true
	}
	// A wheel during the gesture revokes click/fold/Copy资格 but keeps the
	// text range: a press that only wheeled must not toggle or copy.
	wheeled := m.selection.wheeled
	if !m.selection.moved && !wheeled && m.selection.fold.target.kind != foldNone {
		pressed := m.selection.fold
		frozen := m.selection.frozen
		follow := m.selection.follow
		scrolled := m.selection.wheeled
		m.selection.clear()
		m.restoreAfterSelection(frozen, follow, scrolled)
		if hit := m.foldHitAt(message.Mouse()); hit == pressed {
			m.toggleFoldAt(hit)
		}
		return m, nil, true
	}
	if !m.selection.moved && !wheeled && m.selection.code.valid {
		pressed := m.selection.code
		frozen := m.selection.frozen
		follow := m.selection.follow
		scrolled := m.selection.wheeled
		m.selection.clear()
		m.restoreAfterSelection(frozen, follow, scrolled)
		if hit := m.codeHitAt(message.Mouse()); hit == pressed {
			if hit.expansion != nil {
				m.toggleCode(hit)
				return m, nil, true
			}
			command := m.copyText(hit.text)
			return m, command, true
		}
		return m, nil, true
	}
	// Extract from the frozen version before clearing it and before any
	// live refresh can swap in different content.
	selected := selectedFrozenText(&m.selection.frozen, m.selection)
	frozen := m.selection.frozen
	restoreFollow := m.selection.follow
	scrolled := m.selection.wheeled
	m.selection.finish()
	m.selection.clear()
	m.restoreAfterSelection(frozen, restoreFollow, scrolled)
	if strings.TrimSpace(selected) == "" {
		return m, nil, true
	}

	command := m.copyText(selected)
	return m, command, true
}

// restoreAfterSelection brings the live viewport up to date after a gesture.
// Without a manual scroll it keeps the existing bottom-follow behavior with
// one catch-up refresh. After a manual scroll it keeps the final browse
// anchor instead of pulling the user back to the bottom, even though the
// live viewport may still report AtBottom from its pinned press position.
func (m *model) restoreAfterSelection(frozen transcriptViewport, follow bool, scrolled bool) {
	if !scrolled {
		if m.viewportStale {
			// End-of-gesture calibration: rebuild deferred items and restore
			// the pinned bottom position with one walk instead of one per
			// streaming batch. Without deferred batches this stays a no-op so
			// a plain release never rebuilds the viewport.
			m.refreshViewport(follow)
		}
		return
	}
	if len(frozen.items) == 0 {
		if m.viewportStale {
			m.viewportStale = false
			m.viewport.setItems(m.transcriptItems())
		}
		return
	}
	fIdx, fPart, fLine := frozen.index, frozen.part, frozen.line
	fKey := 0
	if fIdx >= 0 && fIdx < len(frozen.items) {
		fKey = frozen.items[fIdx].key
	} else {
		if m.viewportStale {
			m.viewportStale = false
			m.viewport.setItems(m.transcriptItems())
		}
		return
	}
	if m.viewportStale {
		m.viewportStale = false
		m.viewport.setItems(m.transcriptItems())
		for i, item := range m.viewport.items {
			if item.key != fKey {
				continue
			}
			m.viewport.index = i
			parts := m.viewport.itemParts(i)
			part := min(max(fPart, 0), max(len(parts)-1, 0))
			m.viewport.part = part
			height := m.viewport.partHeight(i, part)
			m.viewport.line = min(max(fLine, 0), max(height-1, 0))
			return
		}
		// The anchor key is gone (branch reset etc.); stay at the bottom.
		m.viewport.GotoBottom()
		return
	}
	// Static content: the frozen and live versions share the same items, so
	// sync the live anchor directly without a rebuild or a bottom follow.
	if fIdx < 0 || fIdx >= len(m.viewport.items) {
		return
	}
	m.viewport.index = fIdx
	parts := m.viewport.itemParts(fIdx)
	part := min(max(fPart, 0), max(len(parts)-1, 0))
	m.viewport.part = part
	height := m.viewport.partHeight(fIdx, part)
	m.viewport.line = min(max(fLine, 0), max(height-1, 0))
}

// settleDeferredViewport runs the catch-up refresh once when streaming
// batches deferred layout during a gesture. Outside that window it is a
// no-op; the Update wrapper backstops clear paths without an explicit one.
func (m *model) settleDeferredViewport() {
	if m.viewportStale && !m.selection.active {
		m.refreshViewport(false)
	}
}

func (m *model) copyText(text string) tea.Cmd {
	m.copyNotice = true
	m.copyGeneration++
	generation := m.copyGeneration
	return tea.Batch(tea.SetClipboard(text), tea.Tick(time.Second, func(time.Time) tea.Msg {
		return copyNoticeExpiredMsg(generation)
	}))
}

func (m model) transcriptMousePosition(
	mouse tea.Mouse,
	clampToViewport bool,
	viewportOffset int,
) (transcriptPosition, bool) {
	width := m.viewport.Width()
	height := m.viewport.Height()
	if width <= 0 || height <= 0 {
		return transcriptPosition{}, false
	}

	viewportTop := m.screenLayout().transcript.y
	x := mouse.X - m.horizontalPadding()
	y := mouse.Y - viewportTop
	inside := x >= 0 && x < width && y >= 0 && y < height
	if !inside && !clampToViewport {
		return transcriptPosition{}, false
	}

	x = min(max(x, 0), width-1)
	y = min(max(y, 0), height-1)
	return transcriptPosition{
		row:    viewportOffset + y,
		column: x,
	}, inside
}

// selectedColumnRange maps a content row to its selected display columns.
// Rows strictly between start and end select the full line width; the edge
// rows clamp to the endpoint columns (end already points one past focus).
func selectedColumnRange(
	start, end, row selectionPoint,
	lineWidth int,
) (int, int, bool) {
	if rowBefore(row, start) || rowBefore(end, row) {
		return 0, 0, false
	}
	columnStart := 0
	if rowEqual(row, start) {
		columnStart = start.column
	}
	columnEnd := lineWidth
	if rowEqual(row, end) {
		columnEnd = end.column
	}
	columnStart = min(max(columnStart, 0), lineWidth)
	columnEnd = min(max(columnEnd, 0), lineWidth)
	return columnStart, columnEnd, columnEnd > columnStart
}

// highlightedView renders the frozen window with the current selection.
// The cache is rebuilt when the frozen window scrolls; within one window
// each frame only restyles rows whose selected interval changed.
func (s *transcriptSelection) highlightedView() string {
	if !s.active {
		return s.frozen.View()
	}
	if len(s.frozen.items) == 0 {
		return s.frozen.View()
	}
	h := s.highlight
	if h == nil || h.winIndex != s.frozen.index || h.winPart != s.frozen.part || h.winLine != s.frozen.line {
		h = buildSelectionHighlight(s)
		s.highlight = h
	}

	start, end, selected := s.selectedRange()
	if !selected {
		if h.selected {
			for i := range h.rendered {
				h.restore(i)
			}
			h.selected = false
		}
		return strings.Join(h.rendered, "\n")
	}

	if h.selected {
		for i := range h.rendered {
			oldStart, oldEnd, oldIn := selectedColumnRange(h.lastStart, h.lastEnd, h.points[i], h.widths[i])
			newStart, newEnd, newIn := selectedColumnRange(start, end, h.points[i], h.widths[i])
			if oldIn == newIn && oldStart == newStart && oldEnd == newEnd {
				continue
			}
			if !newIn {
				h.restore(i)
				continue
			}
			h.restyle(i, newStart, newEnd)
		}
	} else {
		for i := range h.rendered {
			columnStart, columnEnd, inRange := selectedColumnRange(start, end, h.points[i], h.widths[i])
			if !inRange {
				continue
			}
			h.restyle(i, columnStart, columnEnd)
		}
	}
	h.selected = true
	h.lastStart, h.lastEnd = start, end
	return strings.Join(h.rendered, "\n")
}

// highlightCurrentWindow is the uncached recompute of the frozen window with
// the current selection. Tests compare it against highlightedView to verify
// the per-window cache without scanning the whole跨屏 range.
func highlightCurrentWindow(frozen *transcriptViewport, selection transcriptSelection) string {
	view := frozen.View()
	start, end, selected := selection.selectedRange()
	if !selected {
		return view
	}
	rows := frozen.visibleRows()
	lines := strings.Split(view, "\n")
	for index, line := range lines {
		var point selectionPoint
		if index < len(rows) {
			point = selectionPoint{item: rows[index].item, part: rows[index].part, line: rows[index].line}
		} else {
			point = selectionPoint{item: -1}
		}
		columnStart, columnEnd, inRange := selectedColumnRange(start, end, point, ansi.StringWidth(line))
		if !inRange {
			continue
		}
		if index < len(rows) {
			if _, _, contentStart, contentEnd, ok := codeSourceRange(&rows[index]); ok {
				columnStart = max(columnStart, contentStart)
				columnEnd = min(columnEnd, contentEnd)
				if columnEnd <= columnStart {
					continue
				}
			}
		}
		lines[index] = lipgloss.StyleRanges(
			line,
			lipgloss.NewRange(
				columnStart,
				columnEnd,
				transcriptSelectionStyle,
			),
		)
	}
	return strings.Join(lines, "\n")
}

// buildSelectionHighlight freezes the current window's geometry both
// highlight paths share: display widths, content identities and the copyable
// code intervals. Rendering the rows themselves stays lazy so a press
// without a drag costs one window split.
func buildSelectionHighlight(s *transcriptSelection) *selectionHighlight {
	view := s.frozen.View()
	lines := strings.Split(view, "\n")
	rows := s.frozen.visibleRows()
	h := &selectionHighlight{
		winIndex:  s.frozen.index,
		winPart:   s.frozen.part,
		winLine:   s.frozen.line,
		original:  lines,
		rendered:  append([]string(nil), lines...),
		widths:    make([]int, len(lines)),
		codeStart: make([]int, len(lines)),
		codeEnd:   make([]int, len(lines)),
		points:    make([]selectionPoint, len(lines)),
	}
	for index, line := range lines {
		h.widths[index] = ansi.StringWidth(line)
		h.codeStart[index] = -1
		if index < len(rows) {
			h.points[index] = selectionPoint{item: rows[index].item, part: rows[index].part, line: rows[index].line}
			if _, _, contentStart, contentEnd, ok := codeSourceRange(&rows[index]); ok {
				h.codeStart[index] = contentStart
				h.codeEnd[index] = contentEnd
			}
		} else {
			h.points[index] = selectionPoint{item: -1}
		}
	}
	return h
}

func (h *selectionHighlight) restore(index int) {
	if index < 0 || index >= len(h.rendered) {
		return
	}
	h.rendered[index] = h.original[index]
}

// restyle applies the selection style to the selected interval of one frozen
// window row, clamping code rows to their copyable source content exactly
// like the uncached path does.
func (h *selectionHighlight) restyle(index, columnStart, columnEnd int) {
	if index < 0 || index >= len(h.rendered) {
		return
	}
	if h.codeStart[index] >= 0 {
		columnStart = max(columnStart, h.codeStart[index])
		columnEnd = min(columnEnd, h.codeEnd[index])
		if columnEnd <= columnStart {
			h.rendered[index] = h.original[index]
			return
		}
	}
	h.rendered[index] = lipgloss.StyleRanges(
		h.original[index],
		lipgloss.NewRange(
			columnStart,
			columnEnd,
			transcriptSelectionStyle,
		),
	)
}

// codeSourceRange maps a frozen viewport row to the copyable source content
// it displays. Panel padding, the header and the line-number gutter are not
// source, so they report ok=false or an intersectable content interval.
func codeSourceRange(snapshotRow *transcriptRow) (*codeBlockPlacement, int, int, int, bool) {
	if snapshotRow == nil || snapshotRow.code.placement == nil {
		return nil, 0, 0, 0, false
	}
	placement := snapshotRow.code.placement
	layout := placement.layout
	if snapshotRow.code.row < 0 || snapshotRow.code.row >= len(layout.rows) {
		return nil, 0, 0, 0, false
	}
	sourceLine := layout.rows[snapshotRow.code.row].sourceLine
	if sourceLine < 0 || sourceLine >= len(layout.block.lines) {
		return nil, 0, 0, 0, false
	}
	contentStart := placement.column + layout.contentColumn
	contentEnd := placement.column + layout.width - 2
	if contentEnd <= contentStart {
		return nil, 0, 0, 0, false
	}
	return placement, sourceLine, contentStart, contentEnd, true
}

func codeSourceLiteral(placement *codeBlockPlacement, sourceLine int) string {
	literal := placement.layout.block.lines[sourceLine]
	if stripped, terminated := strings.CutSuffix(literal, "\n"); terminated {
		literal = strings.TrimSuffix(stripped, "\r")
	}
	return literal
}

// selectedFrozenText extracts the full anchor-to-focus interval from the
// frozen version in order, including offscreen and fast-scroll-skipped rows.
// Drag frames only maintain endpoints and the visible highlight; only the
// release pays this walk plus any on-demand layout of newly reached history.
func selectedFrozenText(
	frozen *transcriptViewport,
	selection transcriptSelection,
) string {
	start, end, selected := selection.selectedRange()
	if !selected || len(frozen.items) == 0 {
		return ""
	}

	type codePick struct {
		placement       *codeBlockPlacement
		sourceLine      int
		line            string
		columnStart     int
		columnEnd       int
		contentStart    int
		contentEnd      int
		full            bool
		pickedStart     int
		pickedEnd       int
		totalVisualRows int
	}
	// Collect per-row picks so wrapped continuations of one source line can
	// be joined without inserting fake newlines or line numbers.
	var picks []any
	curItem, curPart, curLine := start.item, start.part, start.line
	endItem, endPart, endLine := end.item, end.part, end.line
	// Guard against stale endpoints outside the frozen version.
	if curItem < 0 || endItem < 0 || curItem >= len(frozen.items) || endItem >= len(frozen.items) {
		return ""
	}
	for {
		if curItem < 0 || curItem >= len(frozen.items) {
			break
		}
		parts := frozen.itemParts(curItem)
		if curPart < 0 || curPart >= len(parts) {
			break
		}
		height := frozen.partHeight(curItem, curPart)
		if curLine < 0 || curLine >= height {
			break
		}
		var text string
		var code transcriptCodeRow
		partItem := &frozen.itemParts(curItem)[curPart]
		if curLine >= partItem.gap {
			lines := frozen.partLines(curItem, curPart)
			idx := curLine - partItem.gap
			if idx < 0 || idx >= len(lines) {
				break
			}
			text = lines[idx]
			if partItem.codeRows != nil {
				code = partItem.codeRows[idx]
			}
		}
		rowPoint := selectionPoint{item: curItem, part: curPart, line: curLine}
		lineWidth := ansi.StringWidth(text)
		columnStart, columnEnd, inRange := selectedColumnRange(start, end, rowPoint, lineWidth)
		if !inRange {
			picks = append(picks, "")
		} else {
			snapshot := transcriptRow{item: curItem, part: curPart, line: curLine, text: text, code: code}
			placement, sourceLine, contentStart, contentEnd, ok := codeSourceRange(&snapshot)
			if !ok {
				part := ansi.Strip(ansi.Cut(text, columnStart, columnEnd))
				picks = append(picks, strings.TrimRight(part, " "))
			} else {
				contentStart = max(contentStart, 0)
				contentEnd = min(contentEnd, lineWidth)
				pickedStart, pickedEnd := max(columnStart, contentStart), min(columnEnd, contentEnd)
				if pickedEnd <= pickedStart {
					// Gutter or panel padding only: never copy line numbers.
					picks = append(picks, codePick{placement: placement, sourceLine: sourceLine, full: false, pickedStart: -1})
				} else {
					full := columnStart <= contentStart && columnEnd >= contentEnd
					total := 0
					for _, r := range placement.layout.rows {
						if r.sourceLine == sourceLine {
							total++
						}
					}
					picks = append(picks, codePick{
						placement: placement, sourceLine: sourceLine, line: text,
						columnStart: columnStart, columnEnd: columnEnd,
						contentStart: contentStart, contentEnd: contentEnd,
						full: full, pickedStart: pickedStart, pickedEnd: pickedEnd,
						totalVisualRows: total,
					})
				}
			}
		}
		if curItem == endItem && curPart == endPart && curLine == endLine {
			break
		}
		// Advance one visual row; fast wheel jumps simply extend this walk
		// at release instead of appending per-screen text during the drag.
		curLine++
		if curLine < height {
			continue
		}
		curLine = 0
		if curPart+1 < len(parts) {
			curPart++
			continue
		}
		curItem++
		curPart = 0
		// The end is inside the frozen version, so termination above fires
		// before running past the last item.
		if curItem > endItem {
			break
		}
	}

	selectedLines := make([]string, 0, len(picks))
	for i := 0; i < len(picks); {
		pick, isCode := picks[i].(codePick)
		if !isCode {
			selectedLines = append(selectedLines, picks[i].(string))
			i++
			continue
		}
		// Group consecutive visual rows of the same source line.
		j := i + 1
		for j < len(picks) {
			next, ok := picks[j].(codePick)
			if !ok || next.placement != pick.placement || next.sourceLine != pick.sourceLine {
				break
			}
			j++
		}
		group := make([]codePick, 0, j-i)
		for _, p := range picks[i:j] {
			// Gutter-only visual rows contribute nothing to the group.
			if cp, ok := p.(codePick); ok && cp.pickedStart >= 0 {
				group = append(group, cp)
			}
		}
		if len(group) == 0 {
			selectedLines = append(selectedLines, "")
			i = j
			continue
		}
		allFull := len(group) == group[0].totalVisualRows
		for _, g := range group {
			if !g.full {
				allFull = false
				break
			}
		}
		if allFull {
			// Whole source line: copy literal so tabs, trailing spaces
			// and wrapping never leak into the paste.
			selectedLines = append(selectedLines, codeSourceLiteral(pick.placement, pick.sourceLine))
		} else {
			var joined strings.Builder
			for _, g := range group {
				joined.WriteString(ansi.Strip(ansi.Cut(g.line, g.pickedStart, g.pickedEnd)))
			}
			selectedLines = append(selectedLines, strings.TrimRight(joined.String(), " "))
		}
		i = j
	}
	// Drop trailing blank rows (gutter-only drags, panel padding) without
	// stripping intentional trailing spaces from a final code line.
	for len(selectedLines) > 0 && selectedLines[len(selectedLines)-1] == "" {
		selectedLines = selectedLines[:len(selectedLines)-1]
	}
	return strings.Join(selectedLines, "\n")
}

// overlayCopyNotice leaves layout and cursor coordinates unchanged. Hide the
// bubble during another drag so it cannot obscure the text being selected.
func (m model) overlayCopyNotice(content string, width int) string {
	if !m.copyNotice || m.selection.active || m.guardPending != nil {
		return content
	}
	bubble := lipgloss.NewStyle().
		Bold(true).
		Foreground(primaryTextColor).
		Background(inkBlackColor).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(successColor).
		BorderBackground(inkBlackColor).
		Padding(0, 2).
		Render("✓ Copied")
	x := max((width-lipgloss.Width(bubble))/2, 0)
	composerTop := lipgloss.Height(content) - m.verticalPadding() -
		lipgloss.Height(m.footerView(m.layoutWidth())) - lipgloss.Height(m.composerView(m.layoutWidth()))
	y := max(composerTop-lipgloss.Height(bubble), 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(bubble).X(x).Y(y).Z(1),
	).Render()
}
