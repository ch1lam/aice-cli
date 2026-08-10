package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

func (m *model) resizeLayout() {
	width := max(m.width, minimumWidth)
	composerStyle := composerFocusedStyle
	if !m.input.Focused() {
		composerStyle = composerBlurredStyle
	}
	inputWidth := max(width-composerStyle.GetHorizontalFrameSize(), 1)
	m.input.SetWidth(inputWidth)
	m.help.SetWidth(max(width-2, 1))
	m.viewport.SetWidth(width)
	chromeHeight := lipgloss.Height(m.headerView(width)) +
		lipgloss.Height(m.footerView(width)) +
		lipgloss.Height(m.commandMenuView(width)) +
		lipgloss.Height(m.composerView(width))
	viewportHeight := m.height - chromeHeight
	m.viewport.SetHeight(max(viewportHeight, minimumViewport))
}

func (m *model) renderCompletedMarkdown() {
	for index := range m.entries {
		entry := &m.entries[index]
		if entry.kind == entryAssistant && entry.complete {
			entry.rendered = renderMarkdown(entry.text, m.contentWidth())
		}
	}
}

func (m *model) refreshViewport(forceBottom bool) {
	wasAtBottom := m.viewport.AtBottom()
	content := m.transcriptView()
	if content != m.viewport.GetContent() && !m.selection.active {
		m.selection.clear()
	}
	m.viewport.SetContent(content)
	if forceBottom || wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m model) headerView(width int) string {
	innerWidth := max(width-2, 1)
	brand := brandStyle.Render("AICE")
	state := "READY"
	stateColor := successColor
	switch {
	case m.controllerClosed:
		state = "OFFLINE"
		stateColor = errorColor
	case m.running:
		state = "WORKING"
		stateColor = accentColor
	}
	right := lipgloss.NewStyle().Bold(true).Foreground(stateColor).Render("● " + state)
	workspace := "workspace agent"
	workspaceStyle := mutedStyle
	if strings.TrimSpace(m.workingDirectory) != "" {
		workspace = shellWorkingDirectory(m.workingDirectory)
		workspaceStyle = infoStyle
	}
	workspaceWidth := max(
		innerWidth-lipgloss.Width(brand)-lipgloss.Width(right)-5,
		1,
	)
	workspace = truncateTerminalText(workspace, workspaceWidth)
	left := brand + "  " + workspaceStyle.Render(workspace)
	line := left + "  " + right
	return lipgloss.NewStyle().
		Width(innerWidth).
		Padding(0, 1).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(subtleColor).
		Render(line)
}

func (m model) footerView(width int) string {
	innerWidth := max(width-2, 1)
	style := lipgloss.NewStyle().
		Width(innerWidth).
		Padding(0, 1)
	contentWidth := max(innerWidth-style.GetHorizontalFrameSize(), 1)
	rows := make([]string, 0, 2)
	if status := m.statusLine(contentWidth); status != "" {
		rows = append(rows, status)
	}
	if m.help.ShowAll {
		fullHelp := m.help.FullHelpView(m.footerKeys().FullHelp())
		if fullHelp != "" {
			rows = append(rows, fullHelp)
		}
	}
	return style.Render(strings.Join(rows, "\n"))
}

func (m model) footerKeys() keyMap {
	keys := m.keys.forState(m.running, m.acceptsDelivery)
	if m.help.ShowAll {
		keys.help.SetHelp("?", "close")
		// Full help documents contextual shortcuts even while they are inactive.
		keys.queue.SetEnabled(true)
	}
	return keys
}

func (m model) composerView(width int) string {
	style := composerFocusedStyle
	if !m.input.Focused() {
		style = composerBlurredStyle
	}
	contentWidth := max(width-style.GetHorizontalFrameSize(), 1)
	if m.secretInput != nil {
		value := mutedStyle.Render(m.input.Placeholder)
		if count := utf8.RuneCountInString(m.input.Value()); count > 0 {
			value = bodyStyle.Render(strings.Repeat("•", count))
		}
		promptStyle := lipgloss.NewStyle().Foreground(accentColor)
		if !m.input.Focused() {
			promptStyle = lipgloss.NewStyle().Foreground(subtleColor)
		}
		return style.Width(width).Render(
			promptStyle.Render("┃ ") + value,
		)
	}
	parts := make([]string, 0, 3)
	if pending := m.pendingQueueView(contentWidth); pending != "" {
		parts = append(parts, pending, "")
	}
	parts = append(parts, m.input.View())
	return style.Width(width).Render(strings.Join(parts, "\n"))
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
	return !m.running &&
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
	if command.ArgumentHint != "" && command.Menu == nil {
		value += " "
	}
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.commandSelection = 0
	m.commandDismissed = command.ArgumentHint == "" || command.Menu != nil
}

func (m model) commandMenuView(width int) string {
	if m.commandMenu != nil {
		return m.slashCommandSelectionMenuView(width)
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
		}
	}
	return renderSlashMenuRows(
		width,
		"SLASH COMMANDS",
		"↑/↓ select · tab complete · esc close",
		rows,
		min(max(m.commandSelection, 0), len(rows)-1),
	)
}

func (m model) slashCommandSelectionMenuView(width int) string {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return ""
	}
	frame := m.commandMenu.frames[len(m.commandMenu.frames)-1]
	if len(frame.menu.Options) == 0 {
		return ""
	}
	hint := "↑/↓ select · enter choose · esc cancel"
	if len(m.commandMenu.frames) > 1 {
		hint = "↑/↓ select · enter choose · esc back"
	}
	rows := make([]slashMenuRow, len(frame.menu.Options))
	for index, option := range frame.menu.Options {
		rows[index] = slashMenuRow{
			label:       sanitizeToolDetail(option.Label, false),
			description: sanitizeToolDetail(option.Description, false),
			current:     option.Current,
		}
	}
	return renderSlashMenuRows(
		width,
		frame.menu.Title,
		hint,
		rows,
		min(max(frame.selection, 0), len(rows)-1),
	)
}

type slashMenuRow struct {
	label       string
	description string
	current     bool
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
	rendered := make([]string, 0, end-start+2)
	rendered = append(
		rendered,
		labelStyle.Render(strings.ToUpper(title))+"  "+
			mutedStyle.Render(hint),
	)
	for index := start; index < end; index++ {
		row := rows[index]
		prefix := "  "
		rowStyle := slashCommandRowStyle
		labelStyle := labelStyle
		descriptionStyle := mutedStyle
		if row.current {
			prefix = "• "
		}
		if index == selection {
			prefix = "› "
			rowStyle = slashCommandSelectedStyle
			labelStyle = slashCommandSelectedStyle
			descriptionStyle = slashCommandSelectedStyle
		}
		label := truncateTerminalText(
			row.label,
			max(labelWidth-2, 1),
		)
		label += strings.Repeat(" ", max(labelWidth-2-lipgloss.Width(label), 0))
		leading := prefix + labelStyle.Render(label) + "  "
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

func (m model) transcriptView() string {
	parts := make([]transcriptViewPart, 0, len(m.entries)+2)
	for index := 0; index < len(m.entries); {
		entry := m.entries[index]
		if entry.processID != 0 {
			end := index + 1
			for end < len(m.entries) &&
				m.entries[end].processID == entry.processID {
				end++
			}
			process, conclusion := m.processGroupView(index, end)
			if process != "" {
				parts = append(parts, transcriptViewPart{content: process})
			}
			if conclusion != "" {
				parts = append(parts, transcriptViewPart{content: conclusion})
			}
			index = end
			continue
		}

		activeAssistant := m.running &&
			index == m.assistantEntry &&
			!entry.complete
		if content := m.entryView(entry, activeAssistant); content != "" {
			parts = append(parts, transcriptViewPart{
				content: content,
				tool:    entry.kind == entryTool,
			})
		}
		index++
	}
	if activity := m.pendingActivityView(); activity != "" {
		parts = append(parts, transcriptViewPart{content: activity})
	}
	if steering := m.pendingSteeringView(); steering != "" {
		parts = append(parts, transcriptViewPart{content: steering})
	}
	if len(parts) == 0 {
		return m.welcomeView()
	}
	return joinTranscriptViewParts(parts)
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
			content: lipgloss.NewStyle().Padding(0, 1).Render(
				pendingSteerLabelStyle.Render("YOU") + "\n" + body,
			),
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

func (m model) processGroupView(start, end int) (string, string) {
	processID := m.entries[start].processID
	collapsed := false
	for _, group := range m.processGroups {
		if group.id == processID {
			collapsed = group.collapsed
			break
		}
	}

	parts := make([]transcriptViewPart, 0, end-start)
	conclusion := ""
	for index := start; index < end; index++ {
		entry := m.entries[index]
		activeAssistant := m.running &&
			index == m.assistantEntry &&
			!entry.complete
		if entry.kind == entryAssistant && entry.conclusion {
			if reasoning := m.assistantProcessEntryView(
				entry,
				false,
				true,
				false,
			); reasoning != "" {
				parts = append(parts, transcriptViewPart{content: reasoning})
			}
			conclusion = m.assistantProcessEntryView(
				entry,
				activeAssistant,
				false,
				true,
			)
			continue
		}

		content := m.entryView(entry, activeAssistant)
		if entry.kind == entryAssistant {
			content = m.assistantProcessEntryView(
				entry,
				activeAssistant,
				true,
				true,
			)
		}
		if entry.kind == entryAssistant &&
			entry.complete &&
			strings.TrimSpace(entry.thinking) == "" &&
			strings.TrimSpace(entry.text) == "" {
			content = ""
		}
		if content != "" {
			parts = append(parts, transcriptViewPart{
				content: content,
				tool:    entry.kind == entryTool,
			})
		}
	}

	if len(parts) == 0 {
		if conclusion == "" {
			return "", ""
		}
		return "", m.assistantHeaderView(processID) + "\n\n" + conclusion
	}
	header := m.processHeader(start, end, collapsed)
	process := m.assistantHeaderView(processID) + "\n\n" + header
	if collapsed {
		return process, conclusion
	}
	return process + "\n" + joinTranscriptViewParts(parts), conclusion
}

func (m model) processHeader(start, end int, collapsed bool) string {
	icon := "▼"
	action := "ctrl+o to collapse"
	if collapsed {
		icon = "▶"
		action = "ctrl+o to expand"
	}

	hasReasoning := false
	hasIntermediateOutput := false
	toolCalls := 0
	for index := start; index < end; index++ {
		entry := m.entries[index]
		switch entry.kind {
		case entryAssistant:
			hasReasoning = hasReasoning ||
				strings.TrimSpace(entry.thinking) != ""
			hasIntermediateOutput = hasIntermediateOutput ||
				(!entry.conclusion && strings.TrimSpace(entry.text) != "")
		case entryTool:
			toolCalls++
		}
	}

	details := make([]string, 0, 3)
	if hasReasoning {
		details = append(details, "reasoning")
	}
	if hasIntermediateOutput {
		details = append(details, "intermediate output")
	}
	if toolCalls == 1 {
		details = append(details, "1 tool call")
	} else if toolCalls > 1 {
		details = append(details, fmt.Sprintf("%d tool calls", toolCalls))
	}
	if len(details) == 0 {
		details = append(details, "working")
	}

	label := icon + " PROCESS"
	detail := strings.Join(details, " · ")
	innerWidth := max(m.contentWidth()-2, 1)
	detailWidth := innerWidth -
		lipgloss.Width(label) -
		lipgloss.Width(action) -
		4
	if detailWidth > 0 {
		detail = truncateTerminalText(detail, detailWidth)
		return lipgloss.NewStyle().Padding(0, 1).Render(
			labelStyle.Render(label) +
				mutedStyle.Render("  "+detail+"  ") +
				infoStyle.Render(action),
		)
	}

	return lipgloss.NewStyle().Padding(0, 1).Render(
		labelStyle.Render(label) + "\n" +
			infoStyle.Render("  "+action),
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
		bodyWidth := max(width-userStyle.GetHorizontalFrameSize(), 1)
		body := userStyle.Width(bodyWidth).Render(entry.text)
		return lipgloss.NewStyle().Padding(0, 1).Render(
			labelStyle.Render("YOU") + "\n" + body,
		)
	case entryAssistant:
		return m.assistantEntryView(
			entry,
			activeAssistant,
			true,
			true,
		)
	case entryTool:
		icon := m.spinner.View()
		style := lipgloss.NewStyle().Foreground(accentColor)
		if entry.toolDone {
			icon = "✓"
			style = lipgloss.NewStyle().Foreground(successColor)
			if entry.toolError {
				icon = "✕"
				style = errorStyle
			}
		}
		summary := style.Render(icon) + " " +
			toolNameStyle.Render(entry.toolName)
		if entry.toolName != "bash" && entry.toolDetail != "" {
			summary += "  " + mutedStyle.Render(entry.toolDetail)
		}
		if entry.toolName == "bash" && entry.toolDetail != "" {
			summary += "\n" + mutedStyle.Render("$ "+entry.toolDetail)
		}
		return lipgloss.NewStyle().Padding(0, 2).Render(
			summary,
		)
	case entryError:
		return lipgloss.NewStyle().Padding(0, 1).Render(
			errorStyle.Render("✕ Error  " + entry.text),
		)
	case entryNotice:
		return lipgloss.NewStyle().Padding(0, 1).Render(
			noticeStyle.Render("• " + entry.text),
		)
	case entryCommand:
		return lipgloss.NewStyle().Padding(0, 1).Render(
			headerStyle.Render("✦ COMMAND") + "\n" +
				commandOutputStyle.Render(entry.text),
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

func (m model) assistantEntryView(
	entry transcriptEntry,
	activeAssistant bool,
	includeThinking bool,
	includeText bool,
) string {
	content := m.assistantEntryContentView(
		entry,
		activeAssistant,
		includeThinking,
		includeText,
	)
	if content == "" {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(
		m.assistantHeader(entry.processID) + "\n\n" + content,
	)
}

func (m model) assistantProcessEntryView(
	entry transcriptEntry,
	activeAssistant bool,
	includeThinking bool,
	includeText bool,
) string {
	content := m.assistantEntryContentView(
		entry,
		activeAssistant,
		includeThinking,
		includeText,
	)
	if content == "" {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(content)
}

func (m model) assistantEntryContentView(
	entry transcriptEntry,
	activeAssistant bool,
	includeThinking bool,
	includeText bool,
) string {
	width := m.contentWidth()
	parts := make([]string, 0, 2)
	bodyWidth := max(width-assistantBodyStyle.GetHorizontalFrameSize(), 1)
	if includeThinking && strings.TrimSpace(entry.thinking) != "" {
		thinkingWidth := max(
			bodyWidth-thinkingStyle.GetHorizontalFrameSize(),
			1,
		)
		thinking := assistantBodyStyle.Render(
			thinkingStyle.Width(thinkingWidth).Render(
				entry.thinking,
			),
		)
		parts = append(parts, thinking)
	}
	if includeText {
		body := ""
		if entry.rendered != "" {
			body = entry.rendered
		} else if entry.text != "" {
			body = renderMarkdown(entry.text, width)
		}
		if body == "" {
			if activeAssistant {
				body = m.activityIndicator()
			}
		}
		if body != "" {
			parts = append(parts, assistantBodyStyle.Render(body))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func (m model) assistantHeaderView(processID int) string {
	return lipgloss.NewStyle().Padding(0, 1).Render(
		m.assistantHeader(processID),
	)
}

func (m model) assistantHeader(processID int) string {
	header := headerStyle.Render("✦ AICE")
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
	shortcuts := m.help.ShortHelpView(m.footerKeys().ShortHelp())
	model := m.modelStatus()
	fullUsage := m.usageStatus(true)
	compactUsage := m.usageStatus(false)

	usageCandidates := []string{fullUsage}
	if compactUsage != fullUsage {
		usageCandidates = append(usageCandidates, compactUsage)
	}

	rightCandidates := make([]string, 0, len(usageCandidates))
	for _, usage := range usageCandidates {
		rightCandidates = append(
			rightCandidates,
			joinStatusParts(usage, model),
		)
	}
	for _, right := range rightCandidates {
		if line, ok := alignStatusLine(shortcuts, right, width); ok {
			return line
		}
	}

	fallbacks := []string{
		joinStatusParts(compactUsage, model),
		model,
		compactUsage,
	}
	for _, right := range fallbacks {
		if line, ok := alignStatusLine("", right, width); ok {
			return line
		}
	}
	if line, ok := alignStatusLine(shortcuts, "", width); ok {
		return line
	}
	return ""
}

func joinStatusParts(parts ...string) string {
	nonempty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, "  ")
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
	path = filepath.Clean(path)
	if home, err := os.UserHomeDir(); err == nil {
		home = filepath.Clean(home)
		switch {
		case path == home:
			path = "~"
		case strings.HasPrefix(path, home+string(filepath.Separator)):
			path = "~" + strings.TrimPrefix(path, home)
		}
	}
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return '�'
		}
		return character
	}, path)
}

func (m model) modelStatus() string {
	if m.currentModel.ID == "" {
		return ""
	}
	thinking := string(m.thinking)
	if m.thinking == DisplayThinkingDefault {
		thinking = "default"
	}
	return infoStyle.Render(m.currentModel.ID) +
		mutedStyle.Render(" · reasoning "+thinking)
}

func (m model) contentWidth() int {
	return max(max(m.width, minimumWidth)-4, 20)
}

func renderMarkdown(markdown string, width int) string {
	if strings.TrimSpace(markdown) == "" {
		return ""
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(inkMarkdownStyle()),
		glamour.WithWordWrap(max(
			width-assistantBodyStyle.GetHorizontalFrameSize(),
			20,
		)),
	)
	if err != nil {
		return markdown
	}
	rendered, err := renderer.Render(markdown)
	if err != nil {
		return markdown
	}
	return strings.Trim(rendered, "\r\n")
}
