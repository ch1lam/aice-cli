package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/hostpath"
)

func (m *model) resizeLayout() {
	m.resizeGuard()
	width := m.layoutWidth()
	composerStyle := composerFocusedStyle
	if !m.input.Focused() {
		composerStyle = composerBlurredStyle
	}
	inputWidth := max(width-composerStyle.GetHorizontalFrameSize(), 1)
	m.input.SetWidth(inputWidth)
	m.help.SetWidth(max(width-2, 1))
	m.viewport.SetWidth(width)
	if m.guardPending != nil {
		return
	}
	m.chrome = chromeMeasurements{
		header: lipgloss.Height(m.headerView(width)),
		menu:   lipgloss.Height(m.commandMenuView(width)),
		footer: lipgloss.Height(m.footerView(width)),
	}
	if m.reading == nil {
		m.chrome.composer = lipgloss.Height(m.composerViewWithStyle(width, composerBlurredStyle))
	}
	c := m.chrome
	m.viewport.SetHeight(max(m.layoutHeight()-c.header-c.menu-c.composer-c.footer, minimumViewport))
}

func (m *model) refreshViewport(forceBottom bool) {
	// The permission prompt owns the screen. Keep accepting transcript updates,
	// but defer their rendering until sendGuardReply restores the conversation.
	if m.guardPending != nil {
		return
	}
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.setItems(m.transcriptItems())
	if forceBottom || wasAtBottom {
		m.viewport.GotoBottom()
	}
}

type headerLayout struct {
	brand, activity, workspace string
	workspaceX, leftWidth      int
}

func (m model) headerLayout(width int) headerLayout {
	innerWidth := max(width-2, 1)
	brand := brandStyle.Render("AICE")
	state := "READY"
	stateColor := successColor
	sideRunning := m.side.anyRunning()
	if m.side.isVisible {
		brand += mutedStyle.Render(" / ") + infoStyle.Render("BTW")
		state = "MAIN READY"
	} else if unread := m.side.unreadCount(); unread > 0 {
		brand += mutedStyle.Render(" / ") + infoStyle.Render(
			fmt.Sprintf("BTW %d new", unread),
		)
	}
	switch {
	case m.controllerClosed:
		state = "OFFLINE"
		stateColor = errorColor
	case m.running && m.side.isVisible:
		state = "MAIN WORKING"
		stateColor = informationColor
	case m.side.isVisible:
		state = "MAIN READY"
	case m.running && sideRunning:
		state = "MAIN + BTW"
		stateColor = informationColor
	case m.running:
		state = "WORKING"
		stateColor = informationColor
	case sideRunning:
		state = "BTW WORKING"
		stateColor = informationColor
	}
	activity := lipgloss.NewStyle().Bold(true).Foreground(stateColor).Render("● " + state)
	workspace := "workspace agent"
	if strings.TrimSpace(m.workingDirectory) != "" {
		workspace = shellWorkingDirectory(m.workingDirectory)
	}
	contextWidth := m.contextHeaderWidth()
	leftWidth := innerWidth
	if contextWidth > 0 {
		leftWidth = max(leftWidth-contextWidth-2, 0)
	}
	workspaceWidth := max(leftWidth-lipgloss.Width(brand)-lipgloss.Width(activity)-4, 0)
	return headerLayout{
		brand: brand, activity: activity,
		workspace:  truncateTerminalText(workspace, workspaceWidth),
		workspaceX: 1 + lipgloss.Width(brand) + 2,
		leftWidth:  leftWidth,
	}
}

func (m model) headerView(width int) string {
	if m.reading != nil {
		label := "HISTORY · READ ONLY · active branch"
		if m.reading.otherBranch {
			label = "HISTORY · READ ONLY · other branch"
		}
		if m.reading.directory {
			label = "HISTORY · USER QUESTIONS"
		}
		return lipgloss.NewStyle().Width(width).Padding(0, 1, 1).Render(ansi.Truncate(label, width-2, "…"))
	}
	layout := m.headerLayout(width)
	left := layout.brand
	if layout.workspace != "" {
		left += "  " + m.workspaceHeaderView(layout)
	}
	left += "  " + layout.activity
	line := ansi.Truncate(left, layout.leftWidth, "…")
	if m.contextHeaderWidth() > 0 {
		right := m.contextHeaderView()
		line += strings.Repeat(" ", max(width-2-lipgloss.Width(line)-lipgloss.Width(right), 0)) + right
	}
	return lipgloss.NewStyle().Width(width).Padding(0, 1, 1).Render(line)
}

func (m model) footerView(width int) string {
	if m.reading != nil {
		help := m.inputHelp(width, m.help.ShowAll)
		return mutedStyle.Width(width).Render(ansi.Truncate(help, width, "…"))
	}
	innerWidth := max(width-2, 1)
	style := lipgloss.NewStyle().
		Width(innerWidth).
		Padding(0, 1)
	contentWidth := max(innerWidth-style.GetHorizontalFrameSize(), 1)
	rows := make([]string, 0, 2)
	status := m.statusLine(contentWidth)
	if m.side.isVisible {
		status = m.sideStatusLine(contentWidth)
	}
	if status != "" {
		rows = append(rows, status)
	}
	if m.help.ShowAll {
		fullHelp := m.inputFullHelp(contentWidth)
		if fullHelp != "" {
			rows = append(rows, fullHelp)
		}
	}
	return style.Render(strings.Join(rows, "\n"))
}

// composerParts returns the rows rendered inside the composer frame, in
// order: an optional pending-queue notice followed by the input field.
// Attached paste placeholders render inline tinted, still occupying exactly
// their visible width so the terminal cursor stays aligned.
func (m model) composerParts(contentWidth int) []string {
	parts := make([]string, 0, 3)
	if !m.side.isVisible {
		if pending := m.pendingQueueView(contentWidth); pending != "" {
			parts = append(parts, pending, "")
		}
		if m.inputNotice != "" {
			parts = append(parts, noticeStyle.Width(contentWidth).Render(m.inputNotice))
		}
	}
	parts = append(parts, m.highlightPasteTokens(m.commandInputView(contentWidth)))
	return parts
}

func (m model) composerView(width int) string {
	if m.reading != nil {
		return ""
	}
	style := composerBlurredStyle
	if m.input.Focused() && (m.composerActive || m.composerHovered(width)) {
		style = composerFocusedStyle
	}
	return m.composerViewWithStyle(width, style)
}

func (m model) composerViewWithStyle(width int, style lipgloss.Style) string {
	contentWidth := max(width-style.GetHorizontalFrameSize(), 1)
	if m.secretInput != nil || m.authInput != nil {
		value := mutedStyle.Render(m.input.Placeholder)
		if count := utf8.RuneCountInString(m.input.Value()); count > 0 {
			value = bodyStyle.Render(strings.Repeat("•", min(count, contentWidth)))
		}
		return style.Width(width).Render(value)
	}
	parts := m.composerParts(contentWidth)
	frame := style.Width(width).Render(strings.Join(parts, "\n"))
	label := m.modelStatus(max(width-6, 1))
	if label == "" {
		return frame
	}
	label = " " + label + " "
	lines := strings.Split(frame, "\n")
	last := len(lines) - 1
	x := width - lipgloss.Width(label) - 2
	lines[last] = ansi.Cut(lines[last], 0, x) + label + ansi.Cut(lines[last], width-2, width)
	return strings.Join(lines, "\n")
}

func (m model) pendingQueueView(width int) string {
	if len(m.pendingDeliveries) == 0 || width <= 0 {
		return ""
	}
	rows := make([]string, 0, len(m.pendingDeliveries))
	for _, delivery := range m.pendingDeliveries {
		if delivery.mode != deliveryQueue {
			continue
		}
		prefix := mutedStyle.Render("  ↳ ")
		textWidth := max(width-lipgloss.Width(prefix), 1)
		preview := truncateTerminalText(
			pendingDeliveryPreview(delivery.text),
			textWidth,
		)
		rows = append(rows, prefix+bodyStyle.Render(preview))
	}
	return strings.Join(rows, "\n")
}

func pendingDeliveryPreview(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	first, rest, multiline := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	if multiline && strings.TrimSpace(rest) != "" {
		return first + "..."
	}
	return first
}

func (m model) slashCommandMenuVisible() bool {
	return !m.side.isVisible &&
		!m.running &&
		m.secretInput == nil &&
		m.commandMenu == nil &&
		!m.commandDismissed &&
		len(m.matchingSlashCommands()) > 0
}

func (m model) matchingSlashCommands() []SlashCommand {
	return matchingSlashCommands(m.commands, m.input.Value())
}

func (m *model) moveSlashCommandSelection(delta int) {
	matches := m.matchingSlashCommands()
	if len(matches) == 0 {
		m.commandSelection = 0
		return
	}
	m.commandSelection = (m.commandSelection + delta + len(matches)) % len(matches)
}

func (m model) selectedSlashCommand() (SlashCommand, bool) {
	matches := m.matchingSlashCommands()
	if len(matches) == 0 {
		return SlashCommand{}, false
	}
	selection := min(max(m.commandSelection, 0), len(matches)-1)
	return matches[selection], true
}

func (m model) hasExactSlashCommand() bool {
	request, slashCommand := parseSlashCommand(m.input.Value())
	if !slashCommand || request.Arguments != "" {
		return false
	}
	_, exists := findSlashCommand(m.commands, request.Name)
	return exists
}

func (m *model) completeSelectedSlashCommand() {
	command, exists := m.selectedSlashCommand()
	if !exists {
		return
	}
	value := "/" + command.Name
	if command.ArgumentHint != "" || command.Menu != nil {
		value += " "
	}
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.commandSelection = 0
	m.commandDismissed = command.ArgumentHint == "" && command.Menu == nil
	m.syncCommandCompletion()
}

func (m model) commandMenuView(width int) string {
	if m.side.menu != nil {
		return m.sideMenuView(width)
	}
	if m.side.confirm != nil {
		return m.sideConfirmView(width)
	}
	if m.side.isVisible {
		return ""
	}
	if m.commandMenu != nil {
		return m.slashCommandSelectionMenuView(width)
	}
	if m.fileCompletionVisible() {
		return m.fileCompletionView(width)
	}
	return m.slashCommandMenuView(width)
}

func (m model) slashCommandMenuView(width int) string {
	if !m.slashCommandMenuVisible() {
		return ""
	}
	matches := m.matchingSlashCommands()
	rows := make([]slashMenuRow, len(matches))
	for index, command := range matches {
		rows[index] = slashMenuRow{
			label:       slashCommandUsage(command),
			description: command.Description,
			query:       strings.TrimPrefix(strings.TrimSpace(m.input.Value()), "/"),
		}
	}
	return renderSlashMenuRows(
		width,
		"",
		"",
		rows,
		min(max(m.commandSelection, 0), len(rows)-1),
	)
}

func (m model) slashCommandSelectionMenuView(width int) string {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return ""
	}
	options := m.matchingCommandOptions()
	if len(options) == 0 {
		return renderSlashMenuRows(width, "", "",
			[]slashMenuRow{{label: "No matching options"}}, -1)
	}
	frame := m.commandMenu.frames[len(m.commandMenu.frames)-1]
	rows := make([]slashMenuRow, len(options))
	request, _ := parseSlashCommand(m.input.Value())
	for index, option := range options {
		rows[index] = slashMenuRow{
			label:       sanitizeToolDetail(option.Label, false),
			description: sanitizeToolDetail(option.Description, false),
			current:     option.Current,
			query:       request.Arguments,
		}
	}
	return renderSlashMenuRows(
		width,
		"",
		"",
		rows,
		min(max(frame.selection, 0), len(rows)-1),
	)
}

type slashMenuRow struct {
	label       string
	description string
	current     bool
	query       string
}

func renderSlashMenuRows(
	width int,
	title string,
	hint string,
	rows []slashMenuRow,
	selection int,
) string {
	if len(rows) == 0 {
		return ""
	}
	start := max(selection-maximumCommandRows+1, 0)
	end := min(start+maximumCommandRows, len(rows))

	style := slashCommandMenuStyle
	innerWidth := max(width-style.GetHorizontalFrameSize(), 1)
	labelWidth := min(max(innerWidth/2, 12), 28)
	for _, row := range rows {
		if row.description == "" {
			labelWidth = min(max(innerWidth-2, 1), max(labelWidth, lipgloss.Width(row.label)+2))
		}
	}
	rendered := make([]string, 0, end-start+2)
	if title != "" || hint != "" {
		rendered = append(rendered, mutedStyle.Render(
			truncateTerminalText(strings.ToUpper(title)+"  "+hint, innerWidth),
		))
	}
	for index := start; index < end; index++ {
		row := rows[index]
		prefix := "  "
		rowStyle := slashCommandRowStyle
		labelStyle := slashCommandRowStyle
		descriptionStyle := mutedStyle
		_, indices := fuzzyMatch(row.label, row.query)
		if row.current {
			row.label += " (active)"
		}
		if index == selection {
			prefix = "› "
			labelStyle = slashCommandSelectedStyle
		}
		label := truncateTerminalText(
			row.label,
			max(labelWidth-2, 1),
		)
		visible := utf8.RuneCountInString(label)
		if label != row.label {
			visible-- // The ellipsis is not a character from the matching label.
		}
		for len(indices) > 0 && indices[len(indices)-1] >= visible {
			indices = indices[:len(indices)-1]
		}
		label += strings.Repeat(" ", max(labelWidth-2-lipgloss.Width(label), 0))
		leading := labelStyle.Render(prefix) + renderFuzzyLabel(label, indices, labelStyle) + rowStyle.Render("  ")
		descriptionWidth := max(innerWidth-lipgloss.Width(leading), 0)
		description := truncateTerminalText(
			row.description,
			descriptionWidth,
		)
		line := leading
		if descriptionWidth > 0 {
			line += descriptionStyle.Render(description)
		}
		rendered = append(rendered, rowStyle.Width(innerWidth).Render(line))
	}
	return style.Width(width).Render(strings.Join(rendered, "\n"))
}

func renderFuzzyLabel(label string, indices []int, style lipgloss.Style) string {
	if len(indices) == 0 {
		return style.Render(label)
	}
	matchedStyle := style.Foreground(secondaryColor)
	var rendered strings.Builder
	runes := []rune(label)
	start := 0
	for len(indices) > 0 && indices[0] < len(runes) {
		index := indices[0]
		rendered.WriteString(style.Render(string(runes[start:index])))
		end := index + 1
		indices = indices[1:]
		for len(indices) > 0 && indices[0] == end && end < len(runes) {
			end++
			indices = indices[1:]
		}
		rendered.WriteString(matchedStyle.Render(string(runes[index:end])))
		start = end
	}
	rendered.WriteString(style.Render(string(runes[start:])))
	return rendered.String()
}

func truncateTerminalText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width-1 {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// transcriptView materializes an explicit snapshot. Live frames use lazy items.
func (m model) transcriptView() string {
	var result strings.Builder
	for i, item := range m.transcriptItems() {
		if i > 0 {
			result.WriteString(strings.Repeat("\n", item.gap+1))
		}
		result.WriteString(item.content().view)
	}
	return result.String()
}

func (m model) hasPendingSteer() bool {
	for _, delivery := range m.pendingDeliveries {
		if delivery.mode == deliverySteer {
			return true
		}
	}
	return false
}

func (m model) pendingSteeringView() string {
	parts := make([]transcriptViewPart, 0, len(m.pendingDeliveries))
	for _, delivery := range m.pendingDeliveries {
		if delivery.mode != deliverySteer {
			continue
		}
		style := pendingSteerStyle.BorderStyle(lipgloss.Border{
			Left: pendingSteerRail(m.steerRailFrame),
		})
		bodyWidth := max(m.contentWidth()-style.GetHorizontalFrameSize(), 1)
		body := style.Width(bodyWidth).Render(delivery.text)
		parts = append(parts, transcriptViewPart{
			content: lipgloss.NewStyle().Padding(0, 1).Render(body),
		})
	}
	return joinTranscriptViewParts(parts)
}

func pendingSteerRail(frame uint8) string {
	switch frame % 4 {
	case 0:
		return "╎"
	case 1, 3:
		return "┊"
	default:
		return "┆"
	}
}

func (m model) processHeader(start, end int, collapsed bool) string {
	star := "✧"
	action := "collapse"
	if collapsed {
		star = "✦"
		action = "expand"
	}
	for _, binding := range m.inputBindings() {
		if binding.action == inputActionProcess && binding.binding.Enabled() {
			action = binding.binding.Help().Key + " to " + action
			break
		}
	}

	toolCalls := 0
	for index := start; index < end; index++ {
		if index < 0 || index >= len(m.entries) {
			continue
		}
		if m.entries[index].kind == entryTool {
			toolCalls++
		}
	}

	details := make([]string, 0, 2)
	if start >= 0 && start < len(m.entries) {
		if duration, timed := m.processDuration(m.entries[start].processID); timed {
			details = append(details, formatRunDuration(duration))
		}
	}
	if toolCalls == 1 {
		details = append(details, "1 tool call")
	} else if toolCalls > 1 {
		details = append(details, fmt.Sprintf("%d tool calls", toolCalls))
	}

	detail := strings.Join(details, " · ")
	innerWidth := max(m.contentWidth()-2, 1)
	detailWidth := innerWidth -
		lipgloss.Width(star) -
		lipgloss.Width(action) -
		4
	if detail == "" {
		return m.transcriptContentView(
			brandStyle.Render(star) + "  " +
				mutedStyle.Render(action),
		)
	}
	if detailWidth > 0 {
		detail = truncateTerminalText(detail, detailWidth)
		return m.transcriptContentView(
			brandStyle.Render(star) +
				mutedStyle.Render("  "+detail+"  ") +
				mutedStyle.Render(action),
		)
	}

	return m.transcriptContentView(
		brandStyle.Render(star) + "\n" +
			mutedStyle.Render(action),
	)
}

func joinTranscriptViewParts(parts []transcriptViewPart) string {
	if len(parts) == 0 {
		return ""
	}

	var view strings.Builder
	for index, part := range parts {
		if index > 0 {
			separator := "\n\n"
			if parts[index-1].tool && part.tool {
				separator = "\n"
			}
			view.WriteString(separator)
		}
		view.WriteString(part.content)
	}
	return view.String()
}

func (m model) entryView(
	entry transcriptEntry,
	activeAssistant bool,
) string {
	width := m.contentWidth()
	switch entry.kind {
	case entryUser:
		return m.userMessageView(entry.text)
	case entryAssistant:
		return m.assistantEntryContent(entry, activeAssistant, true, true, true).view
	case entryTool:
		return m.transcriptContentView(m.toolHeaderView(entry) + "\n\n" + m.toolBodyView(entry))
	case entryError:
		return m.transcriptContentView(
			errorStyle.Render("✕ Error  " + entry.text),
		)
	case entryNotice:
		return m.transcriptContentView(
			noticeStyle.Render("• " + entry.text),
		)
	case entryCommand:
		return lipgloss.NewStyle().Padding(0, 1).Render(
			assistantBodyStyle.Render(headerStyle.Render("✦ COMMAND")) + "\n" +
				commandOutputStyle.Width(width).Render(entry.text),
		)
	default:
		return ""
	}
}

func sanitizeToolDetail(value string, multiline bool) string {
	return strings.Map(func(character rune) rune {
		if multiline && (character == '\n' || character == '\t') {
			return character
		}
		if unicode.IsControl(character) {
			return '�'
		}
		return character
	}, value)
}

func (m model) assistantEntryContent(entry transcriptEntry, active, thinking, text, header bool) transcriptContent {
	var content transcriptContent
	width := m.contentWidth()
	if thinking {
		content.appendText(entry.presentation.thinkingView(entry.thinking, width, !entry.complete))
	}
	if text {
		body := entry.presentation.textContent(entry.text, width, !entry.complete)
		if body.view == "" && active {
			body.view = assistantBodyStyle.Render(m.activityIndicator())
		}
		content.append(body, "\n")
	}
	if content.view == "" {
		return content
	}
	if header {
		heading := transcriptContent{view: assistantBodyStyle.Render(m.assistantHeader(entry.processID))}
		heading.append(content, "\n\n")
		content = heading
	}
	return content.pad(1, 1)
}

func (m model) assistantHeaderView(processID int) string {
	return m.transcriptContentView(
		m.assistantHeader(processID),
	)
}

func (m model) assistantHeader(processID int) string {
	header := brandStyle.Render("✦")
	duration, timed := m.processDuration(processID)
	if !timed {
		return header
	}
	return header + "  " + mutedStyle.Render(formatRunDuration(duration))
}

func (m model) pendingActivityView() string {
	if !m.running || m.hasActiveTool() || m.hasActiveAssistant() {
		return ""
	}
	return m.assistantHeaderView(m.activeProcessID) + "\n\n" +
		lipgloss.NewStyle().Padding(0, 1).Render(
			assistantBodyStyle.Render(m.activityIndicator()),
		)
}

func (m model) hasActiveAssistant() bool {
	if m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		return false
	}
	return !m.entries[m.assistantEntry].complete
}

func (m model) hasActiveTool() bool {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := m.entries[index]
		if entry.kind == entryTool && !entry.toolDone {
			return true
		}
	}
	return false
}

func (m model) showsActivitySpinner() bool {
	if !m.running {
		return false
	}
	if m.hasActiveTool() || !m.hasActiveAssistant() {
		return true
	}
	return m.entries[m.assistantEntry].text == ""
}

func (m model) activityIndicator() string {
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "Working..."
	}
	return m.spinner.View() + " " + mutedStyle.Render(status)
}

func (m model) statusLine(width int) string {
	shortcuts := ""
	if !m.help.ShowAll {
		shortcuts = m.help.ShortHelpView(m.footerKeys().ShortHelp())
	}
	fullUsage := m.usageStatus(true)
	compactUsage := m.usageStatus(false)
	for _, usage := range []string{fullUsage, compactUsage} {
		if line, ok := alignStatusLine(shortcuts, usage, width); ok {
			return line
		}
	}
	// Active controls take priority when usage and shortcuts cannot share a row.
	if shortcuts != "" {
		return mutedStyle.Render(m.inputHelp(width, false))
	}
	for _, usage := range []string{fullUsage, compactUsage} {
		if line, ok := alignStatusLine("", usage, width); ok {
			return line
		}
	}
	if line, ok := alignStatusLine(shortcuts, "", width); ok {
		return line
	}
	return ""
}

func (m model) contextStatus() string {
	usage := m.contextUsage
	if usage == (DisplayContext{}) {
		return ""
	}
	if usage.Window <= 0 || !usage.Known {
		return mutedStyle.Render("?%")
	}
	used := min(max(usage.Tokens, 0), usage.Window)
	percent := 100 * float64(used) / float64(usage.Window)
	style := mutedStyle
	switch {
	case percent >= 90:
		style = errorStyle
	case percent >= 70:
		style = noticeStyle
	}
	return style.Render(fmt.Sprintf("%.2f%%", percent))
}

func alignStatusLine(left, right string, width int) (string, bool) {
	if lipgloss.Width(left) > width {
		return "", false
	}
	if right == "" {
		return left, true
	}

	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if left == "" {
		if rightWidth > width {
			return "", false
		}
		return strings.Repeat(" ", width-rightWidth) + right, true
	}

	const minimumGap = 2
	if leftWidth+minimumGap+rightWidth > width {
		return "", false
	}
	gap := width - leftWidth - rightWidth
	return left + strings.Repeat(" ", gap) + right, true
}

func (m model) usageStatus(includeCache bool) string {
	usage := m.usageAnimation.Value(m.sessionUsage)
	inputTokens := usage.InputTokens
	if !includeCache {
		inputTokens += usage.CacheReadTokens + usage.CacheWriteTokens
	}

	parts := []string{
		"↑" + formatTokens(inputTokens),
		"↓" + formatTokens(usage.OutputTokens),
	}
	if includeCache {
		parts = append(
			parts,
			"R"+formatTokens(usage.CacheReadTokens),
		)
		parts = append(
			parts,
			"W"+formatTokens(usage.CacheWriteTokens),
		)
	}
	parts = append(parts, fmt.Sprintf("$%.3f", usage.TotalCost))
	return mutedStyle.Render(strings.Join(parts, " "))
}

func formatTokens(count int64) string {
	switch {
	case count < 1_000:
		return strconv.FormatInt(count, 10)
	case count < 10_000:
		rounded := math.Round(float64(count)/100) / 10
		return strconv.FormatFloat(rounded, 'f', 1, 64) + "k"
	case count < 1_000_000:
		rounded := int64(math.Round(float64(count) / 1_000))
		return strconv.FormatInt(rounded, 10) + "k"
	case count < 10_000_000:
		rounded := math.Round(float64(count)/100_000) / 10
		return strconv.FormatFloat(rounded, 'f', 1, 64) + "M"
	default:
		rounded := int64(math.Round(float64(count) / 1_000_000))
		return strconv.FormatInt(rounded, 10) + "M"
	}
}

func shellWorkingDirectory(path string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return '�'
		}
		return character
	}, hostpath.HomeDisplay(path))
}

func (m model) modelStatus(width int) string {
	if m.currentModel.ID == "" {
		return ""
	}
	thinking := string(m.thinking)
	if m.thinking == DisplayThinkingDefault {
		thinking = "default"
	}
	suffix := " (" + thinking + ")"
	name := truncateTerminalText(sanitizeToolDetail(m.currentModel.ID, false), max(width-lipgloss.Width(suffix), 1))
	return mutedStyle.Render(name) + reasoningLevelStyle(m.thinking).Render(suffix)
}

func reasoningLevelStyle(level DisplayThinking) lipgloss.Style {
	switch level {
	case DisplayThinkingMax:
		return lipgloss.NewStyle().Foreground(accentColor)
	case DisplayThinkingXHigh:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F5735F"))
	case DisplayThinkingHigh:
		return lipgloss.NewStyle().Foreground(warningColor)
	case DisplayThinkingMedium:
		return lipgloss.NewStyle().Foreground(secondaryColor)
	case DisplayThinkingLow, DisplayThinkingMinimal, DisplayThinkingOff, DisplayThinkingDefault:
		fallthrough
	default:
		return lipgloss.NewStyle().Foreground(mutedTextColor)
	}
}

func (m model) contentWidth() int {
	return max(m.layoutWidth()-4, 20)
}
