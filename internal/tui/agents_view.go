package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

// agentPanelRows is the panel's list: the running children, or with the
// keyboard every retained child and the selected one, and the window
// [start, end) of it that shows. Rendering and pointer hits share it.
type agentPanelRows struct {
	views                []*agentView
	selected, start, end int
}

func (m model) agentPanelRows() agentPanelRows {
	rows := agentPanelRows{views: m.runningAgents(), selected: -1}
	if m.agentsFocused() {
		rows.views, rows.selected = m.agents, m.selectedAgentIndex()
	}
	rows.end = len(rows.views)
	if len(rows.views) > maximumAgentRows {
		rows.end = maximumAgentRows - 1
		if rows.selected >= 0 {
			rows.start, rows.end = listWindow(rows.selected, maximumAgentRows-1, len(rows.views))
		}
	}
	return rows
}

// agentAt is the child whose panel row is under the pointer.
func (m model) agentAt(mouse tea.Mouse) *agentView {
	if !m.agentPanelVisible() {
		return nil
	}
	area := m.screenLayout().agents
	if !area.contains(mouse) {
		return nil
	}
	// The header takes the panel's first row.
	rows := m.agentPanelRows()
	if i := rows.start + mouse.Y - area.y - 1; i >= rows.start && i < rows.end {
		return rows.views[i]
	}
	return nil
}

// agentPanelView lists the running subagents: what each does, on which
// model, for how long. With the keyboard it lists every retained child,
// finished ones with how they ended, and marks the selected one. The row
// under the pointer is marked too; hovering does not move the keyboard.
func (m model) agentPanelView(width int) string {
	if !m.agentPanelVisible() {
		return ""
	}
	list := m.agentPanelRows()
	running := len(m.runningAgents())
	header := " " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprint(running))
	if list.selected >= 0 {
		header = " " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprintf("%d running · %d", running, len(list.views)))
	}
	var hovered *agentView
	if m.pointer.known {
		hovered = m.agentAt(tea.Mouse{X: m.pointer.x, Y: m.pointer.y})
	}
	// Rows keep one gutter, marked or not, so focusing moves nothing.
	rowWidth := max(width-3, 1)
	rows := []string{header}
	now := time.Now()
	for i := list.start; i < list.end; i++ {
		view := list.views[i]
		gutter := "  "
		switch {
		case i == list.selected:
			gutter = headerStyle.Render("›") + " "
		case view == hovered:
			gutter = mutedStyle.Render("›") + " "
		}
		rows = append(rows, gutter+m.agentRow(view, rowWidth, now))
	}
	if hidden := len(list.views) - (list.end - list.start); hidden > 0 {
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
