package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// maximumAgentRows bounds the panel; the rest are counted on one more row.
const maximumAgentRows = 5

// agentPanelVisible reports whether the subagent panel shows above the
// composer: while children run or it has the keyboard, and not while a menu,
// dialog, side thread or reader owns that space.
func (m model) agentPanelVisible() bool {
	return m.reading == nil && m.guard.request == nil && !m.side.isVisible && m.question == nil &&
		m.commandMenuView(m.layoutWidth()) == "" && (m.agentsFocused() || len(m.runningAgents()) > 0)
}

// agentPanelView lists the running subagents: what each does, on which
// model, for how long. With the keyboard it lists every retained child,
// finished ones with how they ended, and marks the selected one.
func (m model) agentPanelView(width int) string {
	if !m.agentPanelVisible() {
		return ""
	}
	running := m.runningAgents()
	views, selected := running, -1
	header := " " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprint(len(running)))
	if m.agentsFocused() {
		views, selected = m.agents, m.selectedAgentIndex()
		header = " " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprintf("%d running · %d", len(running), len(m.agents)))
	}
	innerWidth := max(width-2, 1)
	rows := []string{header}
	now := time.Now()
	start, end := 0, len(views)
	if len(views) > maximumAgentRows {
		end = maximumAgentRows - 1
		if selected >= 0 {
			start, end = listWindow(selected, maximumAgentRows-1, len(views))
		}
	}
	for i := start; i < end; i++ {
		gutter, rowWidth := " ", innerWidth
		if selected >= 0 {
			gutter, rowWidth = "  ", innerWidth-1
			if i == selected {
				gutter = headerStyle.Render("›") + " "
			}
		}
		rows = append(rows, gutter+m.agentRow(views[i], rowWidth, now))
	}
	if hidden := len(views) - (end - start); hidden > 0 {
		rows = append(rows, "   "+mutedStyle.Render(fmt.Sprintf("+%d more", hidden)))
	}
	return strings.Join(rows, "\n")
}

// agentRow is one child: its description and activity, or how it ended, on
// the left; its model and elapsed time on the right.
func (m model) agentRow(view *agentView, width int, now time.Time) string {
	total := view.elapsed
	if view.running {
		total += now.Sub(view.startedAt)
	}
	// Whole seconds: the row redraws with the spinner, not every millisecond.
	elapsed := "0s"
	if d := total.Truncate(time.Second); d > 0 {
		elapsed = formatRunDuration(d)
	}
	right := strings.TrimSpace(sanitizeSingleLineText(view.start.Model.ID) + " " + elapsed)
	left := m.spinner.View() + " " + toolNameStyle.Render(sanitizeSingleLineText(view.start.Description))
	if view.running {
		if view.activity != "" {
			left += mutedStyle.Render(" · " + view.activity)
		}
		if view.tools > 0 {
			left += mutedStyle.Render(" · " + count(view.tools, "tool"))
		}
	} else {
		status := view.status()
		icon, style := skillStyle.Render("✓"), mutedStyle
		switch {
		case status.failed:
			icon, style = errorStyle.Render("✗"), errorStyle
		case status.stopped:
			icon = mutedStyle.Render("■")
		}
		left = icon + " " + toolNameStyle.Render(sanitizeSingleLineText(view.start.Description)) + style.Render(" · "+status.text)
	}
	available := width - ansi.StringWidth(right) - 2
	if available < 8 {
		return ansi.Truncate(left, width, "…")
	}
	left = ansi.Truncate(left, available, "…")
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	return left + strings.Repeat(" ", max(gap, 1)) + mutedStyle.Render(right)
}

// agentCallLine shows the state of the child an agent call started below
// the call's heading.
func agentCallLine(entry transcriptEntry, width int) string {
	status := entry.toolAgent
	if status.text == "" && !status.running {
		return ""
	}
	style := mutedStyle
	if status.failed {
		style = errorStyle
	}
	text := status.text
	if status.running {
		text = "running · " + text
	}
	return ansi.Truncate(style.Render("↳ "+text), max(width, 1), "…")
}
