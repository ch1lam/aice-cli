package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// renderDetails styles application-structured details by tone. Text is
// sanitized before styling, so details never carry terminal controls.
func renderDetails(details []interaction.Detail, width int) string {
	rows := make([]string, 0, len(details))
	for _, detail := range details {
		indent := strings.Repeat(" ", detail.Indent)
		rendered := bodyStyle.Render(sanitizeSingleLineText(detail.Text))
		switch {
		case detail.Heading != "":
			rendered = labelStyle.Render(sanitizeSingleLineText(detail.Heading))
		case detail.Key != "":
			rendered = mutedStyle.Render(sanitizeSingleLineText(detail.Key)+": ") +
				detailToneStyle(detail.Tone).Render(sanitizeSingleLineText(detail.Value))
		}
		rows = append(rows, ansi.Wrap(indent+rendered, max(1, width), ""))
	}
	return strings.Join(rows, "\n")
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
