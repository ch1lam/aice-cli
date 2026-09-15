package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func editDiffView(diff interaction.DiffDisplay, width int, expanded bool) string {
	if diff.StatsKnown && diff.Text == "" && !diff.Truncated {
		return blockPanel(mutedStyle.Render("No changes"), width)
	}
	limit := 12
	if expanded {
		limit = 2000
	}
	// Replayed metadata is untrusted too. Bound before splitting or escaping.
	text := writePrefix(diff.Text, 64*1024)
	limited := diff.Truncated || len(text) < len(diff.Text)
	rows := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	folded := len(rows) > limit
	if folded {
		rows = rows[:limit]
		limited = limited || expanded
	}
	clipped := false
	inner := max(width-4, 1)
	oldLine, newLine := 0, 0
	showNumbers := inner >= 36
	for i, row := range rows {
		style := bodyStyle.Background(panelBlackColor)
		oldNumber, newNumber := "", ""
		switch {
		case strings.HasPrefix(row, "@@ "):
			oldLine, newLine = diffHunkStart(row)
			style = infoStyle.Background(panelBlackColor)
		case strings.HasPrefix(row, "+"):
			style = lipgloss.NewStyle().Foreground(successColor).Background(lipgloss.Color("#202D20"))
			if newLine > 0 {
				newNumber = strconv.Itoa(newLine)
				newLine++
			}
		case strings.HasPrefix(row, "-"):
			style = errorStyle.Background(lipgloss.Color("#341B1C"))
			if oldLine > 0 {
				oldNumber = strconv.Itoa(oldLine)
				oldLine++
			}
		case strings.HasPrefix(row, " "):
			if oldLine > 0 && newLine > 0 {
				oldNumber, newNumber = strconv.Itoa(oldLine), strconv.Itoa(newLine)
				oldLine++
				newLine++
			}
		}
		row = escapeDiffRow(row)
		gutter := ""
		if showNumbers {
			gutter = fmt.Sprintf("%6s %6s │ ", oldNumber, newNumber)
		}
		available := max(inner-ansi.StringWidth(gutter), 1)
		if ansi.StringWidth(row) > available {
			clipped = true
			row = ansi.Truncate(row, available, "…")
		}
		rows[i] = style.Width(inner).Render(gutter + row)
	}
	result := blockPanel(strings.Join(rows, "\n"), width)
	if folded && !expanded {
		result += "\n" + mutedStyle.Render("… more diff · ctrl+o expand")
	}
	if clipped {
		result += "\n" + mutedStyle.Render("… long lines clipped to terminal width")
	}
	if limited {
		result += "\n" + noticeStyle.Render("… diff incomplete · display limit reached")
	}
	return strings.TrimPrefix(result, "\n")
}

func diffHunkStart(row string) (int, int) {
	fields := strings.Fields(row)
	if len(fields) < 4 {
		return 0, 0
	}
	oldStart, _, _ := strings.Cut(strings.TrimPrefix(fields[1], "-"), ",")
	newStart, _, _ := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
	oldLine, oldErr := strconv.Atoi(oldStart)
	newLine, newErr := strconv.Atoi(newStart)
	if oldErr != nil || newErr != nil || oldLine < 0 || newLine < 0 || oldLine > 1000000 || newLine > 1000000 {
		return 0, 0
	}
	return oldLine, newLine
}

// Escape controls instead of applying them, retaining visible CRLF differences,
// and direction/format controls. Invalid UTF-8 displays as replacement runes.
// Escape backslashes too so
// literal escape-looking source remains distinguishable from actual controls.
func escapeDiffRow(row string) string {
	var out strings.Builder
	for _, r := range strings.ToValidUTF8(row, "�") {
		switch {
		case r == '\\':
			out.WriteString(`\\`)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			quoted := strconv.QuoteRuneToASCII(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
