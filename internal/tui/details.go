package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// renderDetails styles application-structured details by tone. Keys of one
// run of key/value lines share a column and long values wrap under it. Text
// is sanitized before styling, so details never carry terminal controls.
func renderDetails(details []interaction.Detail, width int) string {
	width = max(1, width)
	rows := make([]string, 0, len(details))
	for i := 0; i < len(details); i++ {
		detail := details[i]
		indent := strings.Repeat(" ", detail.Indent)
		switch {
		case detail.Heading != "":
			rows = append(rows, ansi.Wrap(indent+labelStyle.Render(sanitizeSingleLineText(detail.Heading)), width, ""))
		case detail.Key != "":
			end, column := i, 0
			for ; end < len(details) && details[end].Key != "" && details[end].Indent == detail.Indent; end++ {
				column = max(column, ansi.StringWidth(sanitizeSingleLineText(details[end].Key)))
			}
			column = min(column, width/3)
			for ; i < end; i++ {
				rows = append(rows, detailPair(details[i], column, width))
			}
			i--
		default:
			rows = append(rows, ansi.Wrap(indent+detailToneStyle(detail.Tone).Render(sanitizeSingleLineText(detail.Text)), width, ""))
		}
	}
	return strings.Join(rows, "\n")
}

func detailPair(detail interaction.Detail, column, width int) string {
	key := ansi.Truncate(sanitizeSingleLineText(detail.Key), column, "…")
	start := detail.Indent + column + 2
	style := detailToneStyle(detail.Tone)
	lines := strings.Split(ansi.Wrap(sanitizeSingleLineText(detail.Value), max(1, width-start), ""), "\n")
	row := strings.Repeat(" ", detail.Indent) + mutedStyle.Render(key) +
		strings.Repeat(" ", start-detail.Indent-ansi.StringWidth(key)) + style.Render(lines[0])
	for _, line := range lines[1:] {
		row += "\n" + strings.Repeat(" ", start) + style.Render(line)
	}
	return row
}

func detailToneStyle(tone interaction.DetailTone) lipgloss.Style {
	switch tone {
	case interaction.ToneTitle:
		return headerStyle
	case interaction.ToneReference:
		return infoStyle
	case interaction.ToneGood:
		return skillStyle
	case interaction.ToneWarning:
		return noticeStyle
	case interaction.ToneBad:
		return errorStyle
	case interaction.ToneMuted:
		return mutedStyle
	}
	return bodyStyle
}

// toneGlyph marks a status value so its state survives monochrome terminals.
func toneGlyph(tone interaction.DetailTone) string {
	switch tone {
	case interaction.ToneGood:
		return "● "
	case interaction.ToneWarning:
		return "▲ "
	case interaction.ToneBad:
		return "✕ "
	case interaction.ToneMuted:
		return "○ "
	}
	return ""
}
