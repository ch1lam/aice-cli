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
		style, nameStyle, detailStyle = transcriptHoverStyle, transcriptHoverStyle, transcriptHoverStyle
	}
	heading := style.Render(icon) + " " + nameStyle.Render(entry.toolName)
	stats := toolDiffStats(entry) + toolLineStats(entry)
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
	return m.toolBodyContent(entry).view
}

func (m model) toolBodyContent(entry transcriptEntry) transcriptContent {
	width := m.contentWidth()
	var content transcriptContent
	if entry.toolDetail != "" && !toolHasPath(entry.toolName) {
		prefix := ""
		if entry.toolName == "bash" {
			prefix = "$ "
		}
		content.appendText(mutedStyle.Render(prefix + entry.toolDetail))
	}
	hasDiff := toolHasDiff(entry)
	if entry.writePreview != nil && !hasDiff {
		content.append(entry.writePreview.contentView(width, entry.toolExpanded), "\n")
	}
	if hasDiff {
		content.appendText(editDiffView(entry.toolDiff, width, entry.toolExpanded))
	}
	if entry.toolOutput.Available {
		body, limited := toolOutputSource(entry)
		language := "text"
		if entry.toolName == "read" && !entry.toolError {
			language = toolCodeLanguage(entry.toolDetail)
		}
		// A mutation's short outcome belongs below its diff, not in a second panel.
		if hasDiff {
			rows := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
			for i, row := range rows {
				rows[i] = escapeCodeRow(row)
			}
			content.appendText(mutedStyle.Render(strings.Join(rows, "\n")))
		} else {
			content.append(newCodeBlock(body, language).layout(codeBlockOptions{
				width: width, emptyText: "(empty output)", incomplete: limited || entry.toolTruncation.Reason != "",
				hideSummary: true,
			}).content(), "\n")
		}
		if limited {
			content.appendText(noticeStyle.Render("… output display limit reached (64 KiB / 2000 lines)"))
		}
	} else if entry.toolDone && content.view == "" {
		content.appendText(mutedStyle.Render("(output unavailable)"))
	}
	if entry.toolDone && entry.toolTruncation.Reason != "" {
		content.appendText(noticeStyle.Render(toolTruncationStatus(entry.toolTruncation)))
	}
	if content.view == "" {
		content.appendText(mutedStyle.Render("Waiting for result…"))
	}
	return content
}

func toolHasDiff(entry transcriptEntry) bool {
	return entry.toolDone && !entry.toolError && (entry.toolDiff.Text != "" || entry.toolDiff.Truncated || entry.toolDiff.StatsKnown)
}

func toolOutputSource(entry transcriptEntry) (string, bool) {
	source := writePrefix(entry.toolOutput.Text, 64*1024)
	limited := entry.toolOutput.Truncated || len(source) < len(entry.toolOutput.Text)
	source, linesLimited := codeLinePrefix(source, 2000)
	return source, limited || linesLimited
}

// Header counts share the same bounded source as the panel, without rendering
// hidden tool bodies merely to measure their line count.
func toolLineStats(entry transcriptEntry) string {
	if toolHasDiff(entry) {
		return ""
	}
	var source string
	var incomplete bool
	if entry.writePreview != nil {
		var known, omitted bool
		source, _, known, omitted = entry.writePreview.visibleSource(entry.toolExpanded)
		if !known {
			return ""
		}
		incomplete = omitted || !entry.writePreview.known
	} else if entry.toolOutput.Available {
		source, incomplete = toolOutputSource(entry)
		incomplete = incomplete || entry.toolTruncation.Reason != ""
	} else {
		return ""
	}
	count := strings.Count(source, "\n")
	if source != "" && !strings.HasSuffix(source, "\n") {
		count++
	}
	label := fmt.Sprintf("%d lines", count)
	if count == 1 {
		label = "1 line"
	}
	if incomplete {
		label += " · partial"
	}
	return mutedStyle.Render("  (" + label + ")")
}
