package tui

import (
	"fmt"
	"path"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) toolHeaderView(entry transcriptEntry) string {
	return m.toolHeaderStyled(entry, false)
}

func (m model) toolHeaderStyled(entry transcriptEntry, hovered bool) string {
	icon := m.spinner.View()
	style := infoStyle
	if entry.toolPreparing {
		icon = "…"
	}
	if entry.toolDone {
		icon, style = "✓", lipgloss.NewStyle().Foreground(successColor)
		if entry.toolError {
			icon, style = "✕", errorStyle
		}
	}
	nameStyle, detailStyle := toolNameStyle, mutedStyle
	if hovered {
		style, nameStyle, detailStyle = transcriptHoverStyle, transcriptHoverStyle.Bold(true), transcriptHoverStyle
	}
	heading := style.Render(icon) + " " + nameStyle.Render(entry.toolName)
	stats := toolDiffStats(entry)
	if entry.toolDetail != "" {
		switch entry.toolName {
		case "read", "ls", "find", "grep", "write", "edit", "skill":
			if hovered || entry.toolExpanded {
				detailStyle = toolTargetStyle
			}
		}
		detail := entry.toolDetail
		if toolHasPath(entry.toolName) && !entry.toolExpanded {
			detail = path.Base(strings.ReplaceAll(detail, `\`, "/"))
		}
		if entry.toolExpanded && toolHasPath(entry.toolName) {
			heading += "  " + detailStyle.Render(detail)
		} else {
			detail = strings.Join(strings.Fields(detail), " ")
			available := m.contentWidth() - 2 - ansi.StringWidth(heading) - ansi.StringWidth(stats) - 2
			heading += "  " + detailStyle.Render(ansi.Truncate(detail, max(available, 1), "…"))
		}
	}
	heading += stats
	if entry.toolPreparing {
		previewStyle := mutedStyle
		if hovered {
			previewStyle = transcriptHoverStyle
		}
		heading += previewStyle.Render("  preview · not executed")
	}
	return heading
}

func toolHasPath(name string) bool {
	switch name {
	case "read", "ls", "find", "grep", "write", "edit":
		return true
	}
	return false
}

func toolDiffStats(entry transcriptEntry) string {
	if !entry.toolDone || entry.toolError || (entry.toolName != "edit" && entry.toolName != "write") {
		return ""
	}
	diff := entry.toolDiff
	added, removed := diff.Added, diff.Removed
	if !diff.StatsKnown {
		if diff.Truncated || len(diff.Text) > 64*1024 {
			return mutedStyle.Render("  diff incomplete")
		}
		if diff.Text == "" {
			return ""
		}
		// Older Sessions have complete hunks but no stored counts.
		for row := range strings.SplitSeq(writePrefix(diff.Text, 64*1024), "\n") {
			if strings.HasPrefix(row, "+") {
				added++
			} else if strings.HasPrefix(row, "-") {
				removed++
			}
		}
	}
	return "  " + lipgloss.NewStyle().Foreground(successColor).Render(fmt.Sprintf("+%d", added)) +
		" " + errorStyle.Render(fmt.Sprintf("-%d", removed))
}

func (m model) toolBodyView(entry transcriptEntry) string {
	width := m.contentWidth()
	var parts []string
	if entry.toolDetail != "" && !toolHasPath(entry.toolName) {
		prefix := ""
		if entry.toolName == "bash" {
			prefix = "$ "
		}
		parts = append(parts, mutedStyle.Render(prefix+entry.toolDetail))
	}
	hasDiff := entry.toolDone && !entry.toolError && (entry.toolDiff.Text != "" || entry.toolDiff.Truncated || entry.toolDiff.StatsKnown)
	if entry.writePreview != nil && !hasDiff {
		parts = append(parts, entry.writePreview.view(width, entry.toolExpanded))
	}
	if hasDiff {
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
			rows[i] = escapeToolOutputRow(row)
		}
		body := strings.Join(rows, "\n")
		if body == "" {
			body = "(empty output)"
		}
		language := "text"
		if entry.toolName == "read" && !entry.toolError {
			language = toolCodeLanguage(entry.toolDetail)
		}
		// A mutation's short outcome belongs below its diff, not in a second panel.
		if hasDiff {
			parts = append(parts, mutedStyle.Render(body))
		} else {
			parts = append(parts, toolCodeView(body, language, width))
		}
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
