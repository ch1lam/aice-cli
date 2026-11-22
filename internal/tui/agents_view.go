package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// maximumAgentRows bounds the panel; the rest are counted on one more row.
const maximumAgentRows = 5

// agentPanelVisible reports whether the running subagents show above the
// composer: not while a menu, dialog, side thread or reader owns that space.
func (m model) agentPanelVisible() bool {
	return m.reading == nil && m.guard.request == nil && !m.side.isVisible && m.question == nil &&
		m.commandMenuView(m.layoutWidth()) == "" && len(m.runningAgents()) > 0
}

// agentPanelView lists the running subagents: what each does, on which
// model, for how long.
func (m model) agentPanelView(width int) string {
	if !m.agentPanelVisible() {
		return ""
	}
	running := m.runningAgents()
	innerWidth := max(width-2, 1)
	rows := []string{" " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprint(len(running)))}
	now := time.Now()
	for i, view := range running {
		if i == maximumAgentRows-1 && len(running) > maximumAgentRows {
			rows = append(rows, "   "+mutedStyle.Render(fmt.Sprintf("+%d more", len(running)-i)))
			break
		}
		rows = append(rows, " "+m.agentRow(view, innerWidth, now))
	}
	return strings.Join(rows, "\n")
}

// agentRow is one running child: its description and activity on the left,
// its model and elapsed time on the right.
func (m model) agentRow(view *agentView, width int, now time.Time) string {
	// Whole seconds: the row redraws with the spinner, not every millisecond.
	elapsed := "0s"
	if d := (view.elapsed + now.Sub(view.startedAt)).Truncate(time.Second); d > 0 {
		elapsed = formatRunDuration(d)
	}
	right := strings.TrimSpace(sanitizeSingleLineText(view.start.Model.ID) + " " + elapsed)
	left := m.spinner.View() + " " + toolNameStyle.Render(sanitizeSingleLineText(view.start.Description))
	if view.activity != "" {
		left += mutedStyle.Render(" · " + view.activity)
	}
	if view.tools > 0 {
		left += mutedStyle.Render(" · " + count(view.tools, "tool"))
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
