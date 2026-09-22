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
		if hit := m.foldHitAt(message.Mouse()); hit == pressed {
			m.toggleFoldAt(hit)
		}
		return m, nil, true
	}
	if !m.selection.moved && m.selection.code.valid {
		pressed := m.selection.code
		m.selection.clear()
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
	m.selection.clear()
	if strings.TrimSpace(selected) == "" {
		return m, nil, true
	}

	command := m.copyText(selected)
	return m, command, true
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
