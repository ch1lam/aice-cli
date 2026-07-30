package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type transcriptPosition struct {
	row    int
	column int
}

type transcriptSelection struct {
	anchor         transcriptPosition
	focus          transcriptPosition
	viewportOffset int
	viewportView   string
	active         bool
	moved          bool
}

func (s *transcriptSelection) begin(
	position transcriptPosition,
	viewportOffset int,
	viewportView string,
) {
	s.anchor = position
	s.focus = position
	s.viewportOffset = viewportOffset
	s.viewportView = viewportView
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

	m.selection.begin(position, viewportOffset, m.viewport.View())
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

	m.status = "Selected text copied"
	return m, tea.SetClipboard(selected), true
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

	viewportTop := lipgloss.Height(m.headerView(max(m.width, minimumWidth)))
	x := mouse.X
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

	selectedLines := make([]string, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		line := lines[row-viewportOffset]
		columnStart, columnEnd, inRange := selectedLineRange(
			start,
			end,
			row,
			ansi.StringWidth(line),
		)
		if !inRange {
			selectedLines = append(selectedLines, "")
			continue
		}
		part := ansi.Strip(ansi.Cut(line, columnStart, columnEnd))
		selectedLines = append(selectedLines, strings.TrimRight(part, " "))
	}
	return strings.TrimRight(strings.Join(selectedLines, "\n"), " \n")
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
