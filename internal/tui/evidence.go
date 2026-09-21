package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// toolEvidenceView lists the sources a web tool recorded. Titles and URLs are
// untrusted external text: control sequences are escaped and rows are clipped
// to the panel width. Nothing here opens links or re-parses tool output.
func toolEvidenceView(display interaction.EvidenceDisplay, width int) string {
	if len(display.Sources) == 0 && len(display.Warnings) == 0 {
		return ""
	}
	rows := make([]string, 0, len(display.Sources)+len(display.Warnings)+1)
	if len(display.Sources) > 0 {
		label := fmt.Sprintf("%d sources", len(display.Sources))
		if len(display.Sources) == 1 {
			label = "1 source"
		}
		rows = append(rows, mutedStyle.Render(label))
	}
	for index, source := range display.Sources {
		title := strings.Join(strings.Fields(escapeCodeRow(source.Title)), " ")
		url := escapeCodeRow(source.URL)
		kinds := ""
		if len(source.Kinds) > 0 {
			kinds = " · " + strings.Join(source.Kinds, ", ")
		}
		row := fmt.Sprintf("%d. ", index+1)
		if title != "" {
			row += title + " — "
		}
		row += url + kinds
		rows = append(rows, mutedStyle.Render(ansi.Truncate(row, max(width, 1), "…")))
	}
	for _, warning := range display.Warnings {
		rows = append(rows, noticeStyle.Render(ansi.Truncate("warning: "+strings.Join(strings.Fields(escapeCodeRow(warning)), " "), max(width, 1), "…")))
	}
	return strings.Join(rows, "\n")
}
