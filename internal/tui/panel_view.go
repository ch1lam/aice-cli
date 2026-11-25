package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// maximumPanelRows bounds each panel section; the rest are counted on one
// more row.
const maximumPanelRows = 5

// panelItem is one row of the panel below the composer: a running shell
// task or a subagent. Exactly one of the two is set.
type panelItem struct {
	task  *interaction.TaskInfo
	agent *agentView
}

func (item panelItem) id() string {
	if item.task != nil {
		return item.task.ID
	}
	return item.agent.id
}

// shellTasks are the running background shell commands; subagent tasks
// show as subagents.
func (m model) shellTasks() []interaction.TaskInfo {
	var shells []interaction.TaskInfo
	for _, task := range m.tasks {
		if task.Kind == interaction.TaskShell {
			shells = append(shells, task)
		}
	}
	return shells
}

// panelSections are the panel's lists in screen order: shell tasks, then
// subagents — the running ones, or with the keyboard every retained child.
func (m model) panelSections() [2][]panelItem {
	var sections [2][]panelItem
	for _, task := range m.shellTasks() {
		sections[0] = append(sections[0], panelItem{task: &task})
	}
	agents := m.runningAgents()
	if m.panelFocus {
		agents = m.agents
	}
	for _, view := range agents {
		sections[1] = append(sections[1], panelItem{agent: view})
	}
	return sections
}

// panelItems is the panel as one selectable list, top to bottom.
func (m model) panelItems() []panelItem {
	sections := m.panelSections()
	return append(sections[0], sections[1]...)
}

// panelLine is one painted row of the panel: a section header, an item, or
// the count of the items a section leaves out. Painting and pointer hits
// share the lines.
type panelLine struct {
	header string
	item   *panelItem
	more   int
}

func (m model) panelLines() []panelLine {
	selected := ""
	if m.panelFocused() {
		selected = m.panelSelected
	}
	var lines []panelLine
	for section, items := range m.panelSections() {
		if len(items) == 0 {
			continue
		}
		header := " " + headerStyle.Render("Shell tasks") + " " + mutedStyle.Render(fmt.Sprint(len(items)))
		if section == 1 {
			running := len(m.runningAgents())
			header = " " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprint(running))
			if m.panelFocus {
				header = " " + headerStyle.Render("Subagents") + " " + mutedStyle.Render(fmt.Sprintf("%d running · %d", running, len(items)))
			}
		}
		lines = append(lines, panelLine{header: header})
		start, end := 0, len(items)
		if len(items) > maximumPanelRows {
			end = maximumPanelRows - 1
			for i, item := range items {
				if item.id() == selected {
					start, end = listWindow(i, maximumPanelRows-1, len(items))
				}
			}
		}
		for i := start; i < end; i++ {
			lines = append(lines, panelLine{item: &items[i]})
		}
		if hidden := len(items) - (end - start); hidden > 0 {
			lines = append(lines, panelLine{more: hidden})
		}
	}
	return lines
}

// panelVisible reports whether the panel shows below the composer: while
// shell tasks or children run or it has the keyboard, and not while a menu,
// dialog, side thread or reader owns that space.
func (m model) panelVisible() bool {
	return m.reading == nil && m.guard.request == nil && !m.side.isVisible && m.question == nil &&
		m.commandMenuView(m.layoutWidth()) == "" &&
		(m.panelFocused() || len(m.runningAgents()) > 0 || len(m.shellTasks()) > 0)
}

// panelItemAt is the item whose row is under the pointer.
func (m model) panelItemAt(mouse tea.Mouse) *panelItem {
	if !m.panelVisible() {
		return nil
	}
	area := m.screenLayout().panel
	if !area.contains(mouse) {
		return nil
	}
	if lines := m.panelLines(); mouse.Y-area.y < len(lines) {
		return lines[mouse.Y-area.y].item
	}
	return nil
}

// panelView lists the running shell tasks and subagents: what each does and
// for how long. With the keyboard it lists every retained child, finished
// ones with how they ended, and marks the selected row. The row under the
// pointer is marked too; hovering does not move the keyboard.
func (m model) panelView(width int) string {
	if !m.panelVisible() {
		return ""
	}
	hovered := ""
	if m.pointer.known {
		if item := m.panelItemAt(tea.Mouse{X: m.pointer.x, Y: m.pointer.y}); item != nil {
			hovered = item.id()
		}
	}
	selected := ""
	if m.panelFocused() {
		selected = m.panelSelected
	}
	// Rows keep one gutter, marked or not, so focusing moves nothing.
	rowWidth := max(width-3, 1)
	now := time.Now()
	var rows []string
	for _, line := range m.panelLines() {
		switch {
		case line.item == nil && line.more > 0:
			rows = append(rows, "   "+mutedStyle.Render(fmt.Sprintf("+%d more", line.more)))
		case line.item == nil:
			rows = append(rows, line.header)
		default:
			gutter := "  "
			switch line.item.id() {
			case selected:
				gutter = headerStyle.Render("›") + " "
			case hovered:
				gutter = mutedStyle.Render("›") + " "
			}
			if line.item.task != nil {
				rows = append(rows, gutter+m.taskRow(*line.item.task, rowWidth, now))
			} else {
				rows = append(rows, gutter+m.agentRow(line.item.agent, rowWidth, now))
			}
		}
	}
	return strings.Join(rows, "\n")
}

// taskRow is one shell task: why it runs, or its command, on the left; how
// long it has run on the right.
func (m model) taskRow(task interaction.TaskInfo, width int, now time.Time) string {
	elapsed := "0s"
	if d := now.Sub(task.Started).Truncate(time.Second); d > 0 {
		elapsed = formatRunDuration(d)
	}
	name := sanitizeSingleLineText(task.Description)
	command := strings.Join(strings.Fields(sanitizeSingleLineText(task.Command)), " ")
	if name == "" {
		name, command = command, ""
	}
	left := m.spinner.View() + " " + toolNameStyle.Render(name)
	if command != "" {
		left += mutedStyle.Render(" · " + command)
	}
	return alignRow(left, mutedStyle.Render(elapsed), width)
}

// alignRow puts right at the end of a row of width, truncating left first.
func alignRow(left, right string, width int) string {
	available := width - ansi.StringWidth(right) - 2
	if available < 8 {
		return ansi.Truncate(left, width, "…")
	}
	left = ansi.Truncate(left, available, "…")
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	return left + strings.Repeat(" ", max(gap, 1)) + right
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
	return alignRow(left, mutedStyle.Render(right), width)
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
