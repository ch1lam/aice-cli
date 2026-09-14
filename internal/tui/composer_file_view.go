package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m composerInput) View() string {
	view := m.Model.View()
	if len(m.files) == 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	rows := strings.Split(m.Value(), "\n")
	// Ask the pinned textarea for wrap positions instead of duplicating its
	// word wrapping, wide-character, and trailing-space rules. The probe owns
	// its own viewport; rendering must never move the actual editing cursor.
	probe := textarea.New()
	probe.Prompt = ""
	probe.ShowLineNumbers = false
	probe.CharLimit = 0
	probe.SetWidth(m.Width())
	rowTops := make([]int, len(rows))
	for i, row := range rows {
		probe.SetValue(row)
		if i+1 < len(rows) {
			rowTops[i+1] = rowTops[i] + probe.LineInfo().Height
		}
	}
	cursorModel := m.Model
	cursorModel.Focus()
	cursor := cursorModel.Cursor()
	if cursor == nil {
		return view
	}
	top := rowTops[m.Line()] + m.LineInfo().RowOffset - cursor.Y
	offset := 0
	for rowIndex, row := range rows {
		rowRunes := []rune(row)
		probe.SetValue(row)
		for _, file := range m.files {
			if file.editing || file.start < offset || file.end > offset+len(rowRunes) {
				continue
			}
			for start := file.start - offset; start < file.end-offset; {
				probe.SetCursorColumn(start)
				info := probe.LineInfo()
				end := min(file.end-offset, info.StartColumn+info.Width)
				style := lipgloss.NewStyle().Foreground(secondaryColor)
				if start == file.start-offset {
					end = start + 1
					style = lipgloss.NewStyle().Foreground(mutedTextColor)
				}
				if end <= start {
					break
				}
				y := rowTops[rowIndex] + info.RowOffset - top
				if y >= 0 && y < len(lines) {
					x := info.CharOffset
					width := ansi.StringWidth(string(rowRunes[start:end]))
					lines[y] = tintComposerColumns(lines[y], x, min(x+width, m.Width()), style)
				}
				start = end
			}
		}
		offset += utf8.RuneCountInString(row) + 1
	}
	return strings.Join(lines, "\n")
}

func tintComposerColumns(line string, start, end int, style lipgloss.Style) string {
	return ansi.Cut(line, 0, start) +
		style.Render(ansi.Strip(ansi.Cut(line, start, end))) +
		ansi.Cut(line, end, ansi.StringWidth(line))
}
