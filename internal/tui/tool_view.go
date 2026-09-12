package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) toolHeaderView(entry transcriptEntry) string {
	icon := m.spinner.View()
	style := lipgloss.NewStyle().Foreground(accentColor)
	if entry.toolPreparing {
		icon = "…"
	}
	if entry.toolDone {
		icon, style = "✓", lipgloss.NewStyle().Foreground(successColor)
		if entry.toolError {
			icon, style = "✕", errorStyle
		}
	}
	heading := style.Render(icon) + " " + toolNameStyle.Render(entry.toolName)
	if entry.toolDetail != "" {
		detail := strings.Join(strings.Fields(entry.toolDetail), " ")
		heading += "  " + mutedStyle.Render(ansi.Truncate(detail, max(m.contentWidth()-20, 1), "…"))
	}
	if entry.toolPreparing {
		heading += mutedStyle.Render("  preview · not executed")
	}
	return heading
}

func (m model) toolBodyView(entry transcriptEntry) string {
	width := max(m.contentWidth()-8, 1)
	var parts []string
	if entry.toolDetail != "" {
		prefix := ""
		if entry.toolName == "bash" {
			prefix = "$ "
		}
		parts = append(parts, mutedStyle.Render(prefix+entry.toolDetail))
	}
	if entry.writePreview != nil {
		parts = append(parts, entry.writePreview.view(width, entry.toolExpanded))
	}
	if entry.toolDone && !entry.toolError && (entry.toolDiff.Text != "" || entry.toolDiff.Truncated) {
		parts = append(parts, editDiffView(entry.toolDiff, width, entry.toolExpanded))
	}
	if entry.toolOutput.Available {
		text := writePrefix(entry.toolOutput.Text, 64*1024)
		limited := entry.toolOutput.Truncated || len(text) < len(entry.toolOutput.Text)
		rows := strings.SplitN(strings.TrimSuffix(text, "\n"), "\n", 2001)
		if len(rows) > 2000 {
			rows, limited = rows[:2000], true
		}
		for i, row := range rows {
			rows[i] = escapeDiffRow(row)
		}
		body := strings.Join(rows, "\n")
		if body == "" {
			body = "(empty output)"
		}
		parts = append(parts, mutedStyle.Render(body))
		if limited {
			parts = append(parts, noticeStyle.Render("… output display limit reached (64 KiB / 2000 lines)"))
		}
	} else if entry.toolDone && len(parts) == 0 {
		parts = append(parts, mutedStyle.Render("(output unavailable)"))
	}
	if entry.toolDone && entry.toolTruncation.Reason != "" {
		parts = append(parts, noticeStyle.Render(toolTruncationStatus(entry.toolTruncation)))
	}
	if len(parts) == 0 {
		parts = append(parts, mutedStyle.Render("Waiting for result…"))
	}
	return strings.Join(parts, "\n")
}
