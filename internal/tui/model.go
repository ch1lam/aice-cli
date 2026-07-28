package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	minimumWidth       = 24
	minimumViewport    = 1
	maximumEventBatch  = 64
	inputMaximumHeight = 6
	maximumCommandRows = 6
)

type entryKind uint8

const (
	entryUser entryKind = iota + 1
	entryAssistant
	entryTool
	entryError
	entryNotice
	entryCommand
)

type transcriptEntry struct {
	kind      entryKind
	text      string
	thinking  string
	rendered  string
	complete  bool
	toolID    string
	toolName  string
	toolDone  bool
	toolError bool
}

type model struct {
	requests       chan<- runRequest
	controllerDone <-chan struct{}
	updates        <-chan runUpdate
	cancelRun      context.CancelFunc

	viewport     viewport.Model
	input        textarea.Model
	spinner      spinner.Model
	help         help.Model
	keys         keyMap
	currentModel llm.Model
	thinking     llm.ThinkingLevel
	entries      []transcriptEntry
	commands     []SlashCommand

	width            int
	height           int
	assistantEntry   int
	commandSelection int
	commandDismissed bool
	running          bool
	cancelRequested  bool
	controllerClosed bool
	status           string
}

func newModel(
	requests chan<- runRequest,
	controllerDone <-chan struct{},
	externalCommands ...SlashCommand,
) model {
	input := textarea.New()
	input.Prompt = "┃ "
	input.Placeholder = "Ask about this workspace..."
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = inputMaximumHeight
	input.MaxContentHeight = 40
	input.SetHeight(1)
	input.SetWidth(80)
	inputStyles := textarea.DefaultDarkStyles()
	inputStyles.Focused.Base = bodyStyle
	inputStyles.Focused.Text = bodyStyle
	inputStyles.Focused.CursorLine = bodyStyle
	inputStyles.Focused.CursorLineNumber = mutedStyle
	inputStyles.Focused.EndOfBuffer = mutedStyle
	inputStyles.Focused.LineNumber = mutedStyle
	inputStyles.Focused.Prompt = lipgloss.NewStyle().
		Foreground(accentColor)
	inputStyles.Focused.Placeholder = mutedStyle
	inputStyles.Blurred.Base = bodyStyle
	inputStyles.Blurred.Text = bodyStyle
	inputStyles.Blurred.CursorLine = bodyStyle
	inputStyles.Blurred.CursorLineNumber = mutedStyle
	inputStyles.Blurred.EndOfBuffer = mutedStyle
	inputStyles.Blurred.LineNumber = mutedStyle
	inputStyles.Blurred.Prompt = lipgloss.NewStyle().Foreground(subtleColor)
	inputStyles.Blurred.Placeholder = mutedStyle
	inputStyles.Cursor.Color = secondaryColor
	input.SetStyles(inputStyles)
	input.Focus()

	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	// Route keyboard scrolling through AICE's key map so the viewport's hidden
	// pager bindings cannot consume composer input.
	view.KeyMap = viewport.KeyMap{}
	view.SoftWrap = true
	view.FillHeight = true

	activity := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(accentColor)),
	)
	helpView := help.New()
	helpView.ShortSeparator = "  "
	helpView.Styles.ShortKey = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	helpView.Styles.ShortDesc = mutedStyle
	helpView.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(subtleColor)
	helpView.Styles.FullKey = helpView.Styles.ShortKey
	helpView.Styles.FullDesc = helpView.Styles.ShortDesc
	helpView.Styles.FullSeparator = helpView.Styles.ShortSeparator

	return model{
		requests:       requests,
		controllerDone: controllerDone,
		viewport:       view,
		input:          input,
		spinner:        activity,
		help:           helpView,
		keys:           newKeyMap(),
		commands:       slashCommandCatalog(externalCommands),
		assistantEntry: -1,
		status:         "Ready",
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		previousContentWidth := m.contentWidth()
		m.width = message.Width
		m.height = message.Height
		m.resizeLayout()
		if m.contentWidth() != previousContentWidth {
			m.renderCompletedMarkdown()
		}
		m.refreshViewport(false)
		return m, nil
	case tea.KeyPressMsg:
		if updated, command, handled := m.handleKey(message); handled {
			return updated, command
		}
		if !m.running {
			command := m.updateInput(message)
			return m, command
		}
	case runStartedMsg:
		m.updates = message.updates
		return m, tea.Batch(waitForRunUpdates(message.updates), m.spinner.Tick)
	case runUnavailableMsg:
		m.controllerClosed = true
		command := m.finishRun(errors.New("TUI run controller stopped"))
		return m, command
	case runBatchMsg:
		return m.applyRunBatch(message)
	case spinner.TickMsg:
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		if m.running {
			return m, command
		}
		return m, nil
	}

	var commands []tea.Cmd
	if m.running {
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		commands = append(commands, command)
	} else {
		command := m.updateInput(message)
		commands = append(commands, command)
	}

	var viewportCommand tea.Cmd
	m.viewport, viewportCommand = m.viewport.Update(message)
	commands = append(commands, viewportCommand)
	return m, tea.Batch(commands...)
}

func (m model) View() tea.View {
	width := max(m.width, minimumWidth)
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.headerView(width),
		m.viewport.View(),
		m.slashCommandMenuView(width),
		m.composerView(width),
		m.footerView(width),
	)

	view := tea.NewView(content)
	view.BackgroundColor = inkBlackColor
	view.ForegroundColor = primaryTextColor
	view.AltScreen = true
	view.WindowTitle = "AICE"
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m model) handleKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if !m.running && m.slashCommandMenuVisible() {
		switch message.Code {
		case tea.KeyUp:
			m.moveSlashCommandSelection(-1)
			return m, nil, true
		case tea.KeyDown:
			m.moveSlashCommandSelection(1)
			return m, nil, true
		case tea.KeyTab:
			m.completeSelectedSlashCommand()
			m.resizeLayout()
			return m, nil, true
		case tea.KeyEscape:
			m.commandDismissed = true
			m.resizeLayout()
			m.refreshViewport(false)
			return m, nil, true
		}
	}

	switch {
	case key.Matches(message, m.keys.interrupt):
		if m.running {
			if m.cancelRun != nil {
				m.cancelRun()
			} else {
				m.cancelRequested = true
			}
			m.status = "Cancelling current response..."
			return m, nil, true
		}
		return m, tea.Quit, true
	case key.Matches(message, m.keys.quit):
		if !m.running && strings.TrimSpace(m.input.Value()) == "" {
			return m, tea.Quit, true
		}
		return m, nil, true
	case key.Matches(message, m.keys.help):
		m.help.ShowAll = !m.help.ShowAll
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	case key.Matches(message, m.keys.newline):
		if !m.running {
			m.input.InsertString("\n")
			m.resizeLayout()
		}
		return m, nil, true
	case key.Matches(message, m.keys.send):
		if m.running {
			return m, nil, true
		}
		if m.slashCommandMenuVisible() && !m.hasExactSlashCommand() {
			m.completeSelectedSlashCommand()
			m.resizeLayout()
			return m, nil, true
		}
		return m.submit()
	case key.Matches(message, m.keys.scroll):
		switch message.Code {
		case tea.KeyPgUp:
			m.viewport.PageUp()
		case tea.KeyPgDown:
			m.viewport.PageDown()
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m *model) updateInput(message tea.Msg) tea.Cmd {
	previousValue := m.input.Value()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	if m.input.Value() != previousValue {
		m.commandSelection = 0
		m.commandDismissed = false
	}
	m.resizeLayout()
	return command
}

func (m model) submit() (model, tea.Cmd, bool) {
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" {
		return m, nil, true
	}
	if request, slashCommand := parseSlashCommand(prompt); slashCommand {
		return m.submitSlashCommand(prompt, request)
	}
	if m.controllerClosed {
		return m, nil, true
	}

	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: prompt})
	m.input.Reset()
	m.commandSelection = 0
	m.commandDismissed = false
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Starting response..."
	m.resizeLayout()
	m.refreshViewport(true)
	return m, startRun(m.requests, m.controllerDone, prompt), true
}

func (m model) submitSlashCommand(
	raw string,
	request SlashCommandRequest,
) (model, tea.Cmd, bool) {
	command, exists := findSlashCommand(m.commands, request.Name)
	if !exists {
		return m.commandError(
			raw,
			fmt.Sprintf(
				"Unknown slash command /%s. Use /help to list commands.",
				request.Name,
			),
		)
	}

	switch command.Name {
	case "help":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.entries = append(
			m.entries,
			transcriptEntry{kind: entryUser, text: raw},
			transcriptEntry{
				kind: entryCommand,
				text: slashCommandHelp(m.commands),
			},
		)
		m.resetCommandInput()
		m.status = "Slash commands listed"
		m.resizeLayout()
		m.refreshViewport(true)
		return m, nil, true
	case "clear":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.entries = nil
		m.resetCommandInput()
		m.status = "Visible transcript cleared; Session history is unchanged"
		m.resizeLayout()
		m.refreshViewport(true)
		return m, nil, true
	case "quit":
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		m.resetCommandInput()
		return m, tea.Quit, true
	}

	if m.controllerClosed {
		return m.commandError(raw, "TUI run controller stopped")
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: raw})
	m.resetCommandInput()
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Running /" + command.Name + "..."
	m.resizeLayout()
	m.refreshViewport(true)
	return m, startSlashCommand(m.requests, m.controllerDone, request), true
}

func (m model) commandUsageError(
	raw string,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	return m.commandError(
		raw,
		"Usage: "+slashCommandUsage(command),
	)
}

func (m model) commandError(
	raw string,
	message string,
) (model, tea.Cmd, bool) {
	m.entries = append(
		m.entries,
		transcriptEntry{kind: entryUser, text: raw},
		transcriptEntry{kind: entryError, text: message},
	)
	m.resetCommandInput()
	m.status = message
	m.resizeLayout()
	m.refreshViewport(true)
	return m, nil, true
}

func (m *model) resetCommandInput() {
	m.input.Reset()
	m.commandSelection = 0
	m.commandDismissed = false
}

func (m model) applyRunBatch(batch runBatchMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	follow := m.viewport.AtBottom()
	contentChanged := false
	finished := false
	for _, update := range batch.updates {
		if update.cancel != nil {
			m.cancelRun = update.cancel
			if m.cancelRequested {
				m.cancelRun()
				m.status = "Cancelling current response..."
			} else {
				m.status = "Thinking..."
			}
			continue
		}
		if strings.TrimSpace(update.output) != "" {
			m.entries = append(m.entries, transcriptEntry{
				kind: entryCommand,
				text: strings.TrimSpace(update.output),
			})
			contentChanged = true
		}
		if update.done {
			commands = append(commands, m.finishRun(update.err))
			finished = true
			continue
		}
		contentChanged = m.applyAgentEvent(update.event) || contentChanged
	}

	if batch.closed {
		m.updates = nil
		if m.running {
			commands = append(commands, m.finishRun(
				errors.New("agent run ended without a terminal update"),
			))
			finished = true
		}
		if contentChanged && !finished {
			m.refreshViewport(follow)
		}
		return m, tea.Batch(commands...)
	}
	if contentChanged && !finished {
		m.refreshViewport(follow)
	}
	if m.updates == nil {
		return m, tea.Batch(commands...)
	}
	commands = append(commands, waitForRunUpdates(m.updates))
	return m, tea.Batch(commands...)
}

func (m *model) applyAgentEvent(event agent.AgentEvent) bool {
	switch event.Type {
	case agent.EventTypeMessageStart:
		if _, ok := event.Message.(llm.AssistantMessage); ok {
			m.entries = append(m.entries, transcriptEntry{kind: entryAssistant})
			m.assistantEntry = len(m.entries) - 1
			return true
		}
	case agent.EventTypeMessageUpdate:
		return m.applyStreamEvent(event.AssistantMessageEvent)
	case agent.EventTypeMessageEnd:
		if message, ok := event.Message.(llm.AssistantMessage); ok {
			m.completeAssistant(message)
			return true
		}
	case agent.EventTypeToolExecutionStart:
		if event.ToolCall != nil {
			m.entries = append(m.entries, transcriptEntry{
				kind:     entryTool,
				toolID:   event.ToolCall.ID,
				toolName: event.ToolCall.Name,
			})
			m.status = "Running " + event.ToolCall.Name + "..."
			return true
		}
	case agent.EventTypeToolExecutionEnd:
		if event.ToolCall != nil {
			m.completeTool(event.ToolCall.ID, event.Err != nil)
			return true
		}
	case agent.EventTypeAgentEnd:
		if event.Err == nil {
			m.status = "Response complete"
		}
	}
	return false
}

func (m *model) applyStreamEvent(event *llm.Event) bool {
	if event == nil || m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		return false
	}
	entry := &m.entries[m.assistantEntry]
	switch event.Type {
	case llm.EventTypeTextDelta:
		entry.text += event.Delta
		m.status = "Responding..."
	case llm.EventTypeThinkingDelta:
		entry.thinking += event.Delta
		m.status = "Thinking..."
	default:
		return false
	}
	return true
}

func (m *model) completeAssistant(message llm.AssistantMessage) {
	if m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		m.entries = append(m.entries, transcriptEntry{kind: entryAssistant})
		m.assistantEntry = len(m.entries) - 1
	}
	entry := &m.entries[m.assistantEntry]
	entry.text, entry.thinking = assistantContent(message)
	entry.complete = true
	entry.rendered = renderMarkdown(entry.text, m.contentWidth())
}

func (m *model) completeTool(callID string, failed bool) {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := &m.entries[index]
		if entry.kind == entryTool && entry.toolID == callID && !entry.toolDone {
			entry.toolDone = true
			entry.toolError = failed
			return
		}
	}
}

func (m *model) finishRun(err error) tea.Cmd {
	m.running = false
	m.cancelRun = nil
	m.cancelRequested = false
	m.assistantEntry = -1
	focus := m.input.Focus()
	if err != nil {
		message := err.Error()
		if errors.Is(err, context.Canceled) {
			message = "Response cancelled"
			m.entries = append(m.entries, transcriptEntry{kind: entryNotice, text: message})
		} else {
			m.entries = append(m.entries, transcriptEntry{kind: entryError, text: message})
		}
		m.status = message
	} else {
		m.status = "Ready for the next prompt"
	}
	m.resizeLayout()
	m.refreshViewport(true)
	return focus
}

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
		lipgloss.Height(m.slashCommandMenuView(width)) +
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
	m.viewport.SetContent(m.transcriptView())
	if forceBottom || wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m model) headerView(width int) string {
	innerWidth := max(width-2, 1)
	left := brandStyle.Render("AICE") + "  " + mutedStyle.Render("workspace agent")
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
	keys := m.keys.forState(m.running)
	helpView := m.help.View(keys)
	style := lipgloss.NewStyle().
		Width(innerWidth).
		Padding(0, 1).
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(subtleColor)
	contentWidth := max(innerWidth-style.GetHorizontalFrameSize(), 1)
	content := m.statusLine(contentWidth)
	if helpView != "" {
		content += "\n" + helpView
	}
	return style.Render(content)
}

func (m model) composerView(width int) string {
	style := composerFocusedStyle
	if !m.input.Focused() {
		style = composerBlurredStyle
	}
	contentWidth := max(width-style.GetHorizontalFrameSize(), 1)
	return style.Width(contentWidth).Render(m.input.View())
}

func (m model) slashCommandMenuVisible() bool {
	return !m.running &&
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
	if command.ArgumentHint != "" {
		value += " "
	}
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.commandSelection = 0
	m.commandDismissed = command.ArgumentHint == ""
}

func (m model) slashCommandMenuView(width int) string {
	if !m.slashCommandMenuVisible() {
		return ""
	}
	matches := m.matchingSlashCommands()
	selection := min(max(m.commandSelection, 0), len(matches)-1)
	start := max(selection-maximumCommandRows+1, 0)
	end := min(start+maximumCommandRows, len(matches))

	style := slashCommandMenuStyle
	innerWidth := max(width-style.GetHorizontalFrameSize(), 1)
	usageWidth := min(max(innerWidth/2, 12), 28)
	rows := make([]string, 0, end-start+2)
	rows = append(
		rows,
		labelStyle.Render("SLASH COMMANDS")+"  "+
			mutedStyle.Render("↑/↓ select · tab complete · esc close"),
	)
	for index := start; index < end; index++ {
		command := matches[index]
		prefix := "  "
		rowStyle := slashCommandRowStyle
		if index == selection {
			prefix = "› "
			rowStyle = slashCommandSelectedStyle
		}
		usage := truncateTerminalText(
			slashCommandUsage(command),
			max(usageWidth-2, 1),
		)
		usage += strings.Repeat(" ", max(usageWidth-2-lipgloss.Width(usage), 0))
		leading := prefix + labelStyle.Render(usage) + "  "
		descriptionWidth := max(innerWidth-lipgloss.Width(leading), 0)
		description := truncateTerminalText(
			command.Description,
			descriptionWidth,
		)
		row := leading
		if descriptionWidth > 0 {
			row += mutedStyle.Render(description)
		}
		rows = append(rows, rowStyle.Width(innerWidth).Render(row))
	}
	return style.Width(width).Render(strings.Join(rows, "\n"))
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
	if len(m.entries) == 0 {
		return m.welcomeView()
	}

	parts := make([]string, 0, len(m.entries))
	for _, entry := range m.entries {
		parts = append(parts, m.entryView(entry))
	}
	return strings.Join(parts, "\n\n")
}

func (m model) welcomeView() string {
	width := max(m.viewport.Width()-8, 20)
	width = min(width, 62)
	cardStyle := lipgloss.NewStyle().
		Width(max(width-6, 1)).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Background(panelBlackColor)
	title := headerStyle.Render("✦  Work with your codebase")
	description := mutedStyle.Render(
		"Ask AICE to trace behavior, explain architecture, or inspect a file.",
	)
	commandHint := mutedStyle.Render("Type / to browse interactive commands.")
	toolLabel := mutedStyle.Render("AVAILABLE TOOLS")
	tools := labelStyle.Render(
		"read   ls   grep   find",
	)
	card := cardStyle.Render(strings.Join(
		[]string{title, description, commandHint, "", toolLabel, tools},
		"\n",
	))
	return lipgloss.Place(
		m.viewport.Width(),
		m.viewport.Height(),
		lipgloss.Center,
		lipgloss.Center,
		card,
	)
}

func (m model) entryView(entry transcriptEntry) string {
	width := m.contentWidth()
	switch entry.kind {
	case entryUser:
		bodyWidth := max(width-userStyle.GetHorizontalFrameSize(), 1)
		body := userStyle.Width(bodyWidth).Render(entry.text)
		return lipgloss.NewStyle().Padding(0, 1).Render(
			labelStyle.Render("YOU") + "\n" + body,
		)
	case entryAssistant:
		parts := []string{headerStyle.Render("✦ AICE")}
		if strings.TrimSpace(entry.thinking) != "" {
			thinkingWidth := max(width-thinkingStyle.GetHorizontalFrameSize(), 1)
			thinking := thinkingStyle.Width(thinkingWidth).Render(
				"REASONING\n" + entry.thinking,
			)
			parts = append(parts, thinking)
		}
		body := entry.text
		if entry.complete && entry.rendered != "" {
			body = entry.rendered
		} else if body != "" {
			body = bodyStyle.Render(body)
		}
		if body == "" {
			body = mutedStyle.Render("Waiting for model output...")
		}
		parts = append(parts, body)
		return lipgloss.NewStyle().Padding(0, 1).Render(strings.Join(parts, "\n"))
	case entryTool:
		icon := m.spinner.View()
		state := "running"
		style := lipgloss.NewStyle().Foreground(accentColor)
		if entry.toolDone {
			icon = "✓"
			state = "done"
			style = lipgloss.NewStyle().Foreground(successColor)
			if entry.toolError {
				icon = "✕"
				state = "failed"
				style = errorStyle
			}
		}
		return lipgloss.NewStyle().Padding(0, 2).Render(
			style.Render(icon) + " " +
				toolNameStyle.Render(entry.toolName) + "  " +
				mutedStyle.Render(state),
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

func (m model) statusLine(width int) string {
	left := mutedStyle.Render("● " + m.status)
	if m.running {
		left = m.spinner.View() + " " + mutedStyle.Render(m.status)
	} else if m.controllerClosed {
		left = errorStyle.Render("● ") + mutedStyle.Render(m.status)
	} else if strings.Contains(strings.ToLower(m.status), "cancel") {
		left = noticeStyle.Render("● ") + mutedStyle.Render(m.status)
	}
	right := m.modelStatus()
	if right == "" {
		return left
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap <= 0 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m model) modelStatus() string {
	if m.currentModel.ID == "" {
		return ""
	}
	thinking := string(m.thinking)
	if m.thinking == llm.ThinkingLevelUnknown {
		thinking = "default"
	}
	return infoStyle.Render(m.currentModel.ID) +
		mutedStyle.Render(" · reasoning "+thinking)
}

func (m model) contentWidth() int {
	return max(max(m.width, minimumWidth)-4, 20)
}

func assistantContent(message llm.AssistantMessage) (string, string) {
	var text, thinking strings.Builder
	for _, part := range message.Content {
		switch part.Type {
		case llm.ContentTypeText:
			text.WriteString(part.Text)
		case llm.ContentTypeThinking:
			thinking.WriteString(part.Text)
		}
	}
	return text.String(), thinking.String()
}

func renderMarkdown(markdown string, width int) string {
	if strings.TrimSpace(markdown) == "" {
		return ""
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(inkMarkdownStyle()),
		glamour.WithWordWrap(max(width, 20)),
	)
	if err != nil {
		return markdown
	}
	rendered, err := renderer.Render(markdown)
	if err != nil {
		return markdown
	}
	return strings.TrimSpace(rendered)
}

type runStartedMsg struct {
	updates <-chan runUpdate
}

type runUnavailableMsg struct{}

type runBatchMsg struct {
	updates []runUpdate
	closed  bool
}

func startRun(
	requests chan<- runRequest,
	controllerDone <-chan struct{},
	prompt string,
) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan runUpdate, runUpdateBuffer)
		select {
		case <-controllerDone:
			return runUnavailableMsg{}
		case requests <- runRequest{prompt: prompt, updates: updates}:
			return runStartedMsg{updates: updates}
		}
	}
}

func startSlashCommand(
	requests chan<- runRequest,
	controllerDone <-chan struct{},
	request SlashCommandRequest,
) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan runUpdate, runUpdateBuffer)
		select {
		case <-controllerDone:
			return runUnavailableMsg{}
		case requests <- runRequest{command: &request, updates: updates}:
			return runStartedMsg{updates: updates}
		}
	}
}

func waitForRunUpdates(updates <-chan runUpdate) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-updates
		if !ok {
			return runBatchMsg{closed: true}
		}

		batch := runBatchMsg{updates: []runUpdate{first}}
		for len(batch.updates) < maximumEventBatch {
			select {
			case update, open := <-updates:
				if !open {
					batch.closed = true
					return batch
				}
				batch.updates = append(batch.updates, update)
			default:
				return batch
			}
		}
		return batch
	}
}
