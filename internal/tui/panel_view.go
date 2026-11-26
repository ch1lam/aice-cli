package tui

import (
	"fmt"
	"image/color"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// maximumPanelRows bounds each panel section; the rest are counted on one
// more row.
const maximumPanelRows = 5

// panelInset is the symmetric left/right margin that tucks the hanging tray
// beneath the composer's rounded corners so both frames stay visually coupled
// without sharing a two-color box-drawing junction.
const panelInset = 1

// panelItem is one row of the tray below the composer: a running shell task
// or a subagent. Exactly one of the two is set.
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

type panelLineKind uint8

const (
	panelLineDivider panelLineKind = iota + 1
	panelLineSummary
	panelLineItem
	panelLineMore
	panelLineBottom
)

// panelLine is one painted row of the hanging tray: an internal section
// divider, the unfocused compact summary, an item row, an overflow count, or
// the closing bottom edge. Painting and pointer hits share the lines.
type panelLine struct {
	kind   panelLineKind
	header string
	item   *panelItem
	more   int
}

func panelSectionTitle(section, total, running int) string {
	if section == 1 {
		return bodyStyle.Bold(true).Render("Subagents") + mutedStyle.Render(fmt.Sprintf(" · %d running / %d", running, total))
	}
	return bodyStyle.Bold(true).Render("Shell tasks") + mutedStyle.Render(" · "+fmt.Sprint(total))
}

// panelBorderTitle returns the first section's title to embed into the left of
// the composer's bottom border while the tray is focused, saving a header row.
func (m model) panelBorderTitle() string {
	if !m.panelVisible() || !m.panelFocused() {
		return ""
	}
	sections := m.panelSections()
	if len(sections[0]) > 0 {
		return panelSectionTitle(0, len(sections[0]), 0)
	}
	if len(sections[1]) > 0 {
		return panelSectionTitle(1, len(sections[1]), len(m.runningAgents()))
	}
	return ""
}

func (m model) panelLines() []panelLine {
	sections := m.panelSections()
	if !m.panelFocused() {
		items := append(sections[0], sections[1]...)
		if len(items) == 0 {
			return nil
		}
		body := panelLine{kind: panelLineSummary}
		if len(items) == 1 {
			body = panelLine{kind: panelLineItem, item: &items[0]}
		}
		return []panelLine{
			body,
			{kind: panelLineBottom},
		}
	}

	selected := m.panelSelected
	var lines []panelLine
	for section, items := range sections {
		if len(items) == 0 {
			continue
		}
		if len(lines) > 0 {
			header := panelSectionTitle(section, len(items), len(m.runningAgents()))
			lines = append(lines, panelLine{kind: panelLineDivider, header: header})
		}
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
			lines = append(lines, panelLine{kind: panelLineItem, item: &items[i]})
		}
		if hidden := len(items) - (end - start); hidden > 0 {
			lines = append(lines, panelLine{kind: panelLineMore, more: hidden})
		}
	}
	if len(lines) > 0 {
		lines = append(lines, panelLine{kind: panelLineBottom})
	}
	return lines
}

// panelVisible reports whether the tray shows below the composer: while
// shell tasks or children run or it has the keyboard, and not while a menu,
// dialog, side thread or reader owns that space.
func (m model) panelVisible() bool {
	return m.reading == nil && m.guard.request == nil && !m.side.isVisible && m.question == nil &&
		m.commandMenuView(m.layoutWidth()) == "" &&
		(m.panelFocused() || len(m.runningAgents()) > 0 || len(m.shellTasks()) > 0)
}

// panelHit identifies an interactive target inside the tray: either a specific
// item row, or the unfocused summary row that expands the tray.
type panelHit struct {
	item   *panelItem
	expand bool
}

func (h panelHit) any() bool { return h.item != nil || h.expand }

// panelHitAt resolves the panel row under the pointer.
func (m model) panelHitAt(mouse tea.Mouse) panelHit {
	if !m.panelVisible() {
		return panelHit{}
	}
	area := m.screenLayout().panel
	if !area.contains(mouse) || mouse.X < area.x+panelInset || mouse.X >= area.x+area.width-panelInset {
		return panelHit{}
	}
	lines := m.panelLines()
	row := mouse.Y - area.y
	if row < 0 || row >= len(lines) {
		return panelHit{}
	}
	switch lines[row].kind {
	case panelLineItem:
		return panelHit{item: lines[row].item}
	case panelLineSummary:
		return panelHit{expand: true}
	default:
		return panelHit{}
	}
}

// panelView renders the recessed hanging tray directly below the composer's
// bottom border. Both left and right edges inset symmetrically by one cell
// with no top border, so composer and tray keep independent focus colors with
// no shared box-drawing junction.
func (m model) panelView(width int) string {
	if !m.panelVisible() {
		return ""
	}
	lines := m.panelLines()
	if len(lines) == 0 {
		return ""
	}
	focused := m.panelFocused()
	wallColor := subtleColor
	if focused {
		wallColor = secondaryColor
	}
	var hovered panelHit
	if m.pointer.known {
		hovered = m.panelHitAt(tea.Mouse{X: m.pointer.x, Y: m.pointer.y})
	}
	trayWidth := max(width-2*panelInset, 4)
	innerWidth := max(trayWidth-4, 1)
	now := time.Now()
	rows := make([]string, 0, len(lines))
	for _, line := range lines {
		switch line.kind {
		case panelLineDivider:
			rows = append(rows, panelSectionDivider(trayWidth, line.header, wallColor))
		case panelLineBottom:
			rows = append(rows, panelBottomRow(trayWidth, wallColor))
		case panelLineMore:
			rows = append(rows, panelBoxRow("  "+mutedStyle.Render(fmt.Sprintf("+%d more", line.more)), innerWidth, nil, wallColor))
		case panelLineSummary:
			var bg color.Color
			if hovered.expand {
				bg = panelBlackColor
			}
			rows = append(rows, panelBoxRow(m.panelSummaryRow(innerWidth, hovered.expand), innerWidth, bg, wallColor))
		case panelLineItem:
			id := line.item.id()
			isSelected := focused && id == m.panelSelected
			isHovered := hovered.item != nil && hovered.item.id() == id
			isArmed := focused && m.panelStopArmed == id
			var bg color.Color
			if isSelected {
				bg = codeStatusColor
			} else if isHovered {
				bg = panelBlackColor
			}
			var content string
			if focused {
				gutter := "  "
				switch {
				case isSelected:
					gutter = lipgloss.NewStyle().Foreground(secondaryColor).Render("›") + " "
				case isHovered:
					gutter = mutedStyle.Render("›") + " "
				}
				rowWidth := max(innerWidth-2, 1)
				if line.item.task != nil {
					content = gutter + m.taskRow(*line.item.task, rowWidth, now, isSelected, isArmed)
				} else {
					content = gutter + m.agentRow(line.item.agent, rowWidth, now, isSelected, isArmed)
				}
			} else {
				if line.item.task != nil {
					content = m.taskRow(*line.item.task, innerWidth, now, false, false)
				} else {
					content = m.agentRow(line.item.agent, innerWidth, now, false, false)
				}
			}
			rows = append(rows, panelBoxRow(content, innerWidth, bg, wallColor))
		}
	}
	return strings.Join(rows, "\n")
}

// panelSectionDivider separates two sections inside the focused tray.
func panelSectionDivider(trayWidth int, label string, borderColor color.Color) string {
	margin := strings.Repeat(" ", panelInset)
	edge := lipgloss.NewStyle().Foreground(borderColor)
	avail := trayWidth - 2
	if avail < 5 || label == "" {
		return margin + edge.Render("├"+strings.Repeat("─", max(avail, 0))+"┤") + margin
	}
	leftBlock := " " + ansi.Truncate(label, avail-4, "…") + " "
	fill := max(avail-1-ansi.StringWidth(leftBlock), 1)
	return margin + edge.Render("├─") + leftBlock + edge.Render(strings.Repeat("─", fill)+"┤") + margin
}

func panelBottomRow(trayWidth int, borderColor color.Color) string {
	margin := strings.Repeat(" ", panelInset)
	return margin + lipgloss.NewStyle().Foreground(borderColor).Render("╰"+strings.Repeat("─", max(trayWidth-2, 0))+"╯") + margin
}

func panelBoxRow(content string, innerWidth int, bg, borderColor color.Color) string {
	margin := strings.Repeat(" ", panelInset)
	content = ansi.Truncate(content, innerWidth, "…")
	if pad := innerWidth - ansi.StringWidth(content); pad > 0 {
		content += strings.Repeat(" ", pad)
	}
	interior := " " + content + " "
	if bg != nil {
		interior = paintCodeBackground(interior, bg)
	}
	wall := lipgloss.NewStyle().Foreground(borderColor).Render("│")
	return margin + wall + interior + wall + margin
}

// panelSummaryRow builds the compact single-row status shown while multiple
// tasks or subagents run and the tray is unfocused.
func (m model) panelSummaryRow(width int, hovered bool) string {
	shells := m.shellTasks()
	agents := m.runningAgents()
	var counts []string
	if len(shells) > 0 {
		noun := "shell task"
		if len(agents) > 0 {
			noun = "shell"
		}
		counts = append(counts, count(len(shells), noun))
	}
	if len(agents) > 0 {
		counts = append(counts, count(len(agents), "subagent"))
	}
	prefix := m.spinner.View() + " " + bodyStyle.Bold(true).Render(strings.Join(counts, " · "))

	cueStyle := mutedStyle
	if hovered {
		cueStyle = lipgloss.NewStyle().Foreground(secondaryColor)
	}
	right := cueStyle.Render("↓ expand")
	if width < ansi.StringWidth(prefix)+ansi.StringWidth(right)+4 {
		right = cueStyle.Render("↓")
	}

	previews := make([]string, 0, len(shells)+len(agents))
	for _, task := range shells {
		name := sanitizeSingleLineText(task.Description)
		if name == "" {
			name = strings.Join(strings.Fields(sanitizeSingleLineText(task.Command)), " ")
		}
		previews = append(previews, name)
	}
	for _, view := range agents {
		name := sanitizeSingleLineText(view.start.Description)
		if activity := compactAgentActivity(view.activity); activity != "" {
			name += " (" + activity + ")"
		}
		previews = append(previews, name)
	}

	available := width - ansi.StringWidth(right) - 2
	if available < 8 {
		return ansi.Truncate(prefix, width, "…")
	}
	left := prefix
	for i, preview := range previews {
		remaining := len(previews) - i - 1
		reserve := 0
		if remaining > 0 {
			reserve = ansi.StringWidth(fmt.Sprintf(" · +%d more", remaining))
		}
		candidate := " · " + preview
		if ansi.StringWidth(left)+ansi.StringWidth(candidate)+reserve <= available {
			left += mutedStyle.Render(candidate)
			continue
		}
		if i == 0 {
			if budget := available - ansi.StringWidth(left) - reserve - ansi.StringWidth(" · "); budget >= 10 {
				left += mutedStyle.Render(" · " + ansi.Truncate(preview, budget, "…"))
				if remaining > 0 {
					left += mutedStyle.Render(fmt.Sprintf(" · +%d more", remaining))
				}
				break
			}
		}
		more := fmt.Sprintf(" · +%d more", len(previews)-i)
		if ansi.StringWidth(left)+ansi.StringWidth(more) <= available {
			left += mutedStyle.Render(more)
		}
		break
	}
	return alignRow(left, right, width)
}

func compactAgentActivity(activity string) string {
	activity = sanitizeSingleLineText(activity)
	if after, ok := strings.CutPrefix(activity, "Running "); ok {
		tool, detail, hasDetail := strings.Cut(strings.TrimSpace(after), " ")
		if hasDetail && toolHasPath(tool) {
			detail = path.Base(strings.ReplaceAll(detail, `\`, "/"))
		}
		if hasDetail {
			return tool + " " + detail
		}
		return tool
	}
	return activity
}

func formatAgentActivity(activity string) string {
	activity = sanitizeSingleLineText(activity)
	if after, ok := strings.CutPrefix(activity, "Running "); ok {
		tool, detail, hasDetail := strings.Cut(strings.TrimSpace(after), " ")
		if hasDetail {
			return toolNameStyle.Render(tool) + mutedStyle.Render(" "+detail)
		}
		return toolNameStyle.Render(tool)
	}
	if activity == "Stopping" || activity == "Retrying" {
		return noticeStyle.Render(activity)
	}
	return mutedStyle.Render(activity)
}

// taskRow is one shell task: why it runs, or its command, on the left; how
// long it has run (or stop confirmation) on the right.
func (m model) taskRow(task interaction.TaskInfo, width int, now time.Time, selected, armed bool) string {
	elapsed := "0s"
	if d := now.Sub(task.Started).Truncate(time.Second); d > 0 {
		elapsed = formatRunDuration(d)
	}
	name := sanitizeSingleLineText(task.Description)
	command := strings.Join(strings.Fields(sanitizeSingleLineText(task.Command)), " ")
	if name == "" {
		name, command = command, ""
	}
	nameStyle := bodyStyle
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	left := m.spinner.View() + " " + nameStyle.Render(name)
	if command != "" {
		left += mutedStyle.Render("  " + command)
	}
	right := mutedStyle.Render(elapsed)
	if armed {
		right = noticeStyle.Bold(true).Render("x again to stop")
	}
	return alignRow(left, right, width)
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
// the left; its model and elapsed time (or stop confirmation) on the right.
func (m model) agentRow(view *agentView, width int, now time.Time, selected, armed bool) string {
	total := view.elapsed
	if view.running {
		total += now.Sub(view.startedAt)
	}
	// Whole seconds: the row redraws with the spinner, not every millisecond.
	elapsed := "0s"
	if d := total.Truncate(time.Second); d > 0 {
		elapsed = formatRunDuration(d)
	}
	rightText := strings.TrimSpace(sanitizeSingleLineText(view.start.Model.ID) + "  " + elapsed)
	right := mutedStyle.Render(rightText)
	if armed {
		right = noticeStyle.Bold(true).Render("x again to stop")
	}
	nameStyle := bodyStyle
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	desc := sanitizeSingleLineText(view.start.Description)
	var left string
	if view.running {
		left = m.spinner.View() + " " + nameStyle.Render(desc)
		if view.activity != "" {
			left += "  " + formatAgentActivity(view.activity)
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
		left = icon + " " + nameStyle.Render(desc) + "  " + style.Render(status.text)
	}
	return alignRow(left, right, width)
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
