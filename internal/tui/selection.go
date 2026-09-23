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

type transcriptSelection struct {
	anchor         transcriptPosition
	focus          transcriptPosition
	viewportOffset int
	viewportView   string
	// rows freezes the visible geometry at press time so a drag copies the
	// same rows it highlights even if streaming reflows the viewport.
	rows   []transcriptRow
	active bool
	moved  bool
	fold   foldHit
	code   codeHit
	// follow records whether the viewport was pinned to the bottom at press
	// time. Layout and following pause while the frozen snapshot owns the
	// screen; a drag release restores the pinned position with one catch-up
	// refresh instead of paying per-batch walks during the gesture.
	follow bool
	// highlight caches the frozen view's per-row geometry and rendered
	// output for the gesture, so each drag frame only restyles rows whose
	// selected interval changed. View/Update copies share it through the
	// pointer; begin rebuilds it and clear drops it.
	highlight *selectionHighlight
}

// selectionHighlight is the per-gesture highlight cache. The frozen view,
// its row widths and the code content intervals never change while the
// gesture is active; only the anchor/focus move, so a frame only needs to
// restyle rows entering or leaving the selected range.
type selectionHighlight struct {
	original []string
	rendered []string
	widths   []int
	// codeStart/codeEnd bound the copyable source content per row.
	// codeStart is negative for non-code rows, where the whole selected
	// interval is highlightable.
	codeStart []int
	codeEnd   []int
	selected  bool
	lastStart transcriptPosition
	lastEnd   transcriptPosition
}

func (s *transcriptSelection) begin(
	position transcriptPosition,
	viewportOffset int,
	viewportView string,
	rows []transcriptRow,
) {
	s.anchor = position
	s.focus = position
	s.viewportOffset = viewportOffset
	s.viewportView = viewportView
	s.rows = rows
	s.active = true
	s.moved = false
	// A press reusing this struct must not inherit the previous gesture's
	// highlight cache: the frozen view and its rows are replaced below.
	s.highlight = nil
}

func (s *transcriptSelection) update(position transcriptPosition) bool {
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
	transcriptPosition,
	transcriptPosition,
	bool,
) {
	if !s.moved {
		return transcriptPosition{}, transcriptPosition{}, false
	}

	if positionBefore(s.anchor, s.focus) {
		return s.anchor, afterCell(s.focus), true
	}
	return s.focus, afterCell(s.anchor), true
}

func positionBefore(left, right transcriptPosition) bool {
	return left.row < right.row ||
		(left.row == right.row && left.column <= right.column)
}

func afterCell(position transcriptPosition) transcriptPosition {
	position.column++
	return position
}

func (m model) handleTranscriptMouseClick(
	message tea.MouseClickMsg,
) (model, tea.Cmd, bool) {
	if message.Button != tea.MouseLeft {
		return m, nil, false
	}

	viewportOffset := m.viewport.YOffset()
	position, inside := m.transcriptMousePosition(
		message.Mouse(),
		false,
		viewportOffset,
	)
	if !inside {
		m.selection.clear()
		return m, nil, false
	}

	m.selection.begin(position, viewportOffset, m.viewport.View(), m.viewport.visibleRows())
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

	position, _ := m.transcriptMousePosition(
		message.Mouse(),
		true,
		m.selection.viewportOffset,
	)
	if !m.selection.update(position) {
		m.selection.clear()
	}
	return m, nil, true
}

func (m model) handleTranscriptMouseRelease(
	message tea.MouseReleaseMsg,
) (model, tea.Cmd, bool) {
	if !m.selection.active || message.Button != tea.MouseLeft {
		return m, nil, false
	}

	position, _ := m.transcriptMousePosition(
		message.Mouse(),
		true,
		m.selection.viewportOffset,
	)
	if !m.selection.update(position) {
		m.selection.clear()
		return m, nil, true
	}
	if !m.selection.moved && m.selection.fold.target.kind != foldNone {
		pressed := m.selection.fold
		m.selection.clear()
		m.settleDeferredViewport()
		if hit := m.foldHitAt(message.Mouse()); hit == pressed {
			m.toggleFoldAt(hit)
		}
		return m, nil, true
	}
	if !m.selection.moved && m.selection.code.valid {
		pressed := m.selection.code
		m.selection.clear()
		m.settleDeferredViewport()
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
	m.selection.finish()

	selected := selectedTranscriptText(
		m.selection.viewportView,
		m.selection,
		m.selection.viewportOffset,
	)
	restoreFollow := m.selection.follow
	m.selection.clear()
	if m.viewportStale {
		// End-of-gesture calibration: rebuild deferred items and restore
		// the pinned bottom position with one walk instead of one per
		// streaming batch. Without deferred batches this stays a no-op so
		// a plain release never rebuilds the viewport. Clicks on folds and
		// code targets settle above and keep their own anchors.
		m.refreshViewport(restoreFollow)
	}
	if strings.TrimSpace(selected) == "" {
		return m, nil, true
	}

	command := m.copyText(selected)
	return m, command, true
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

func highlightTranscriptSelection(
	view string,
	selection transcriptSelection,
	viewportOffset int,
) string {
	start, end, selected := selection.selectedRange()
	if !selected || selection.viewportOffset != viewportOffset {
		return view
	}

	lines := strings.Split(view, "\n")
	for index, line := range lines {
		row := viewportOffset + index
		columnStart, columnEnd, inRange := selectedLineRange(
			start,
			end,
			row,
			ansi.StringWidth(line),
		)
		if !inRange {
			continue
		}
		// Line numbers are display-only. Highlight only the source content
		// so the gutter never looks copyable.
		if _, _, contentStart, contentEnd, ok := codeSourceRange(selection.rowSnapshot(row, viewportOffset)); ok {
			columnStart = max(columnStart, contentStart)
			columnEnd = min(columnEnd, contentEnd)
			if columnEnd <= columnStart {
				continue
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

// highlightedView renders the frozen snapshot with the current selection.
// The cache is built once per gesture from the frozen view; each frame only
// restyles rows whose selected interval changed since the previous frame.
// The result matches highlightTranscriptSelection for the same inputs.
func (s *transcriptSelection) highlightedView() string {
	if !s.active {
		return s.viewportView
	}
	h := s.highlight
	if h == nil {
		h = buildSelectionHighlight(s)
		s.highlight = h
	}

	start, end, selected := s.selectedRange()
	if !selected {
		if h.selected {
			for row := h.lastStart.row; row <= h.lastEnd.row; row++ {
				h.restore(row, s.viewportOffset)
			}
			h.selected = false
		}
		return strings.Join(h.rendered, "\n")
	}

	if h.selected {
		from := min(h.lastStart.row, start.row)
		to := max(h.lastEnd.row, end.row)
		for row := from; row <= to; row++ {
			oldStart, oldEnd, oldIn := selectedLineRange(h.lastStart, h.lastEnd, row, h.width(row, s.viewportOffset))
			newStart, newEnd, newIn := selectedLineRange(start, end, row, h.width(row, s.viewportOffset))
			if oldIn == newIn && oldStart == newStart && oldEnd == newEnd {
				continue
			}
			if !newIn {
				h.restore(row, s.viewportOffset)
				continue
			}
			h.restyle(row, s.viewportOffset, newStart, newEnd)
		}
	} else {
		for row := start.row; row <= end.row; row++ {
			columnStart, columnEnd, inRange := selectedLineRange(start, end, row, h.width(row, s.viewportOffset))
			if !inRange {
				continue
			}
			h.restyle(row, s.viewportOffset, columnStart, columnEnd)
		}
	}
	h.selected = true
	h.lastStart, h.lastEnd = start, end
	return strings.Join(h.rendered, "\n")
}

// buildSelectionHighlight freezes the per-row geometry both highlight paths
// share: display widths and the copyable code intervals. Rendering the rows
// themselves stays lazy so a press without a drag costs one split.
func buildSelectionHighlight(s *transcriptSelection) *selectionHighlight {
	lines := strings.Split(s.viewportView, "\n")
	h := &selectionHighlight{
		original:  lines,
		rendered:  append([]string(nil), lines...),
		widths:    make([]int, len(lines)),
		codeStart: make([]int, len(lines)),
		codeEnd:   make([]int, len(lines)),
	}
	for index, line := range lines {
		h.widths[index] = ansi.StringWidth(line)
		h.codeStart[index] = -1
		row := s.viewportOffset + index
		// rowSnapshot bounds-checks s.rows, so rows without a code
		// placement simply keep the whole-row highlight interval.
		if _, _, contentStart, contentEnd, ok := codeSourceRange(s.rowSnapshot(row, s.viewportOffset)); ok {
			h.codeStart[index] = contentStart
			h.codeEnd[index] = contentEnd
		}
	}
	return h
}

func (h *selectionHighlight) width(row, viewportOffset int) int {
	index := row - viewportOffset
	if index < 0 || index >= len(h.widths) {
		return 0
	}
	return h.widths[index]
}

func (h *selectionHighlight) restore(row, viewportOffset int) {
	index := row - viewportOffset
	if index < 0 || index >= len(h.rendered) {
		return
	}
	h.rendered[index] = h.original[index]
}

// restyle applies the selection style to the selected interval of one frozen
// row, clamping code rows to their copyable source content exactly like
// highlightTranscriptSelection does.
func (h *selectionHighlight) restyle(row, viewportOffset, columnStart, columnEnd int) {
	index := row - viewportOffset
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

func (s transcriptSelection) rowSnapshot(row, viewportOffset int) *transcriptRow {
	index := row - viewportOffset
	if index < 0 || index >= len(s.rows) {
		return nil
	}
	return &s.rows[index]
}

func codeSourceLiteral(placement *codeBlockPlacement, sourceLine int) string {
	literal := placement.layout.block.lines[sourceLine]
	if stripped, terminated := strings.CutSuffix(literal, "\n"); terminated {
		literal = strings.TrimSuffix(stripped, "\r")
	}
	return literal
}

func selectedTranscriptText(
	view string,
	selection transcriptSelection,
	viewportOffset int,
) string {
	start, end, selected := selection.selectedRange()
	if !selected || selection.viewportOffset != viewportOffset {
		return ""
	}

	lines := strings.Split(view, "\n")
	firstVisibleRow := viewportOffset
	lastVisibleRow := viewportOffset + len(lines) - 1
	if start.row < firstVisibleRow || end.row > lastVisibleRow {
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
	// Collect per-viewport-row picks so wrapped continuations of one source
	// line can be joined without inserting fake newlines or line numbers.
	picks := make([]any, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		line := lines[row-viewportOffset]
		lineWidth := ansi.StringWidth(line)
		columnStart, columnEnd, inRange := selectedLineRange(start, end, row, lineWidth)
		if !inRange {
			picks = append(picks, "")
			continue
		}
		placement, sourceLine, contentStart, contentEnd, ok := codeSourceRange(selection.rowSnapshot(row, viewportOffset))
		if !ok {
			part := ansi.Strip(ansi.Cut(line, columnStart, columnEnd))
			picks = append(picks, strings.TrimRight(part, " "))
			continue
		}
		contentStart = max(contentStart, 0)
		contentEnd = min(contentEnd, lineWidth)
		pickedStart, pickedEnd := max(columnStart, contentStart), min(columnEnd, contentEnd)
		if pickedEnd <= pickedStart {
			// Gutter or panel padding only: never copy line numbers.
			picks = append(picks, codePick{placement: placement, sourceLine: sourceLine, full: false, pickedStart: -1})
			continue
		}
		full := columnStart <= contentStart && columnEnd >= contentEnd
		total := 0
		for _, r := range placement.layout.rows {
			if r.sourceLine == sourceLine {
				total++
			}
		}
		picks = append(picks, codePick{
			placement: placement, sourceLine: sourceLine, line: line,
			columnStart: columnStart, columnEnd: columnEnd,
			contentStart: contentStart, contentEnd: contentEnd,
			full: full, pickedStart: pickedStart, pickedEnd: pickedEnd,
			totalVisualRows: total,
		})
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

func selectedLineRange(
	start transcriptPosition,
	end transcriptPosition,
	row int,
	lineWidth int,
) (int, int, bool) {
	if row < start.row || row > end.row {
		return 0, 0, false
	}

	columnStart := 0
	if row == start.row {
		columnStart = start.column
	}
	columnEnd := lineWidth
	if row == end.row {
		columnEnd = end.column
	}
	columnStart = min(max(columnStart, 0), lineWidth)
	columnEnd = min(max(columnEnd, 0), lineWidth)
	return columnStart, columnEnd, columnEnd > columnStart
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
