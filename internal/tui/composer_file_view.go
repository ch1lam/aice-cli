package tui

import (
	"strings"
	"unicode/utf8"

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
	rowStarts := make([]int, len(rows))
	for i := 1; i < len(rows); i++ {
		rowStarts[i] = rowStarts[i-1] + utf8.RuneCountInString(rows[i-1]) + 1
	}
	// Query only row starts: the textarea owns wrapping and scrolling, while
	// whole substrings determine widths without its per-rune x hit testing.
	position := m.PositionAt(0, 0)
	for y := range lines {
		next := m.PositionAt(0, y+1)
		row := []rune(rows[position.Row])
		limit := len(row)
		if next.Row == position.Row {
			limit = next.Col
		}
		offset := rowStarts[position.Row]
		for _, file := range m.files {
			start := max(file.start-offset, position.Col)
			end := min(file.end-offset, limit)
			if file.editing || start >= end {
				continue
			}
			x := ansi.StringWidth(string(row[position.Col:start]))
			if start == file.start-offset {
				lines[y] = tintComposerColumns(lines[y], x, x+1,
					lipgloss.NewStyle().Foreground(mutedTextColor))
				start++
				x++
			}
			if start < end {
				width := ansi.StringWidth(string(row[start:end]))
				lines[y] = tintComposerColumns(lines[y], x, min(x+width, m.Width()),
					lipgloss.NewStyle().Foreground(secondaryColor))
			}
		}
		position = next
	}
	return strings.Join(lines, "\n")
}

func tintComposerColumns(line string, start, end int, style lipgloss.Style) string {
	return ansi.Cut(line, 0, start) +
		style.Render(ansi.Strip(ansi.Cut(line, start, end))) +
		ansi.Cut(line, end, ansi.StringWidth(line))
}
