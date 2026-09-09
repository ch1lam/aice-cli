package tui

import (
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func editDiffView(diff interaction.DiffDisplay, width int, expanded bool) string {
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
	for i, row := range rows {
		style := mutedStyle
		if strings.HasPrefix(row, "+") {
			style = lipgloss.NewStyle().Foreground(successColor)
		} else if strings.HasPrefix(row, "-") {
			style = errorStyle
		}
		row = escapeDiffRow(row)
		if ansi.StringWidth(row) > width {
			clipped = true
			row = ansi.Truncate(row, width, "…")
		}
		rows[i] = style.Render(row)
	}
	result := strings.Join(rows, "\n")
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
