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
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/llm"
)

const (
	minimumWidth         = 24
	minimumViewport      = 1
	maximumEventBatch    = 64
	inputMaximumHeight   = 6
	maximumCommandRows   = 6
	maximumPromptHistory = 100
	defaultPlaceholder   = "Ask about this workspace..."
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
	kind       entryKind
	text       string
	thinking   string
	rendered   string
	complete   bool
	processID  int
	conclusion bool
	toolID     string
	toolName   string
	toolDetail string
	toolDone   bool
	toolError  bool
}

type processGroup struct {
	id        int
	collapsed bool
}

type transcriptViewPart struct {
	content string
	tool    bool
}

type secretInput struct {
	request SlashCommandRequest
	prompt  string
}

type commandMenuFrame struct {
	menu      SlashCommandMenu
	selection int
}

type commandMenuState struct {
	raw     string
	request SlashCommandRequest
	command SlashCommand
	frames  []commandMenuFrame
}

type model struct {
	requests       chan<- runRequest
	controllerDone <-chan struct{}
	updates        <-chan runUpdate
	cancelRun      context.CancelFunc

	viewport         viewport.Model
	selection        transcriptSelection
	input            textarea.Model
	spinner          spinner.Model
	help             help.Model
	keys             keyMap
	currentModel     llm.Model
	thinking         llm.ThinkingLevel
	apiKeyConfigured bool
	sessionUsage     llm.Usage
	usageAnimation   usageAnimation
	welcomeAnimation welcomeAnimation
	workingDirectory string
	entries          []transcriptEntry
	processGroups    []processGroup
	commands         []SlashCommand
	secretInput      *secretInput
	commandMenu      *commandMenuState

	promptHistory []string
	historyIndex  int
	historyDraft  string

	width            int
	height           int
	assistantEntry   int
	activeProcessID  int
	nextProcessID    int
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
	input.Placeholder = defaultPlaceholder
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
		historyIndex:   -1,
		status:         "Ready",
		// The welcome animation starts running at construction; Init() emits
		// its first tick. It pauses once the run starts and resumes on /clear.
		welcomeAnimation: welcomeAnimation{running: true, generation: 1},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.welcomeAnimation.tick())
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.selection.clear()
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
		m.selection.clear()
		if updated, command, handled := m.handleKey(message); handled {
			return updated, command
		}
		if !m.running {
			command := m.updateInput(message)
			return m, command
		}
	case tea.MouseClickMsg:
		if updated, command, handled := m.handleTranscriptMouseClick(message); handled {
			return updated, command
		}
	case tea.MouseMotionMsg:
		if updated, command, handled := m.handleTranscriptMouseMotion(message); handled {
			return updated, command
		}
	case tea.MouseReleaseMsg:
		if updated, command, handled := m.handleTranscriptMouseRelease(message); handled {
			return updated, command
		}
	case tea.MouseWheelMsg:
		m.selection.clear()
	case runStartedMsg:
		m.updates = message.updates
		return m, tea.Batch(waitForRunUpdates(message.updates), m.spinner.Tick)
	case runUnavailableMsg:
		m.controllerClosed = true
		command := m.finishRun(errors.New("TUI run controller stopped"))
		return m, command
	case runBatchMsg:
		return m.applyRunBatch(message)
	case usageAnimationTickMsg:
		return m, m.usageAnimation.Update(message)
	case welcomeTickMsg:
		active := len(m.entries) == 0 && !m.running
		command := m.welcomeAnimation.Update(message, active)
		if active {
			// The logo lives inside the viewport, so its color sweep needs
			// the transcript content re-rendered on every tick.
			m.refreshViewport(false)
		}
		return m, command
	case spinner.TickMsg:
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		if m.running {
			if m.showsActivitySpinner() {
				m.refreshViewport(false)
			}
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
	viewportView := m.viewport.View()
	viewportOffset := m.viewport.YOffset()
	if m.selection.active {
		viewportView = m.selection.viewportView
		viewportOffset = m.selection.viewportOffset
	}
	transcript := highlightTranscriptSelection(
		viewportView,
		m.selection,
		viewportOffset,
	)
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.headerView(width),
		transcript,
		m.commandMenuView(width),
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
	if !m.running && m.secretInput != nil {
		switch {
		case message.Code == tea.KeyEscape,
			key.Matches(message, m.keys.interrupt),
			key.Matches(message, m.keys.quit):
			return m.cancelSecretInput()
		case key.Matches(message, m.keys.newline):
			m.status = "API key must be entered on one line"
			return m, nil, true
		}
	}

	if !m.running && m.commandMenu != nil {
		switch {
		case message.Code == tea.KeyEscape,
			key.Matches(message, m.keys.interrupt),
			key.Matches(message, m.keys.quit):
			return m.backOrCancelCommandMenu()
		case message.Code == tea.KeyUp:
			m.moveCommandMenuSelection(-1)
			return m, nil, true
		case message.Code == tea.KeyDown:
			m.moveCommandMenuSelection(1)
			return m, nil, true
		case message.Code == tea.KeyTab,
			key.Matches(message, m.keys.send):
			return m.selectCommandMenuOption()
		default:
			return m, nil, true
		}
	}

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

	if !m.running &&
		m.secretInput == nil &&
		m.commandMenu == nil &&
		!m.slashCommandMenuVisible() {
		// Up recalls an earlier prompt, Down moves forward again. A multi-line
		// draft never switches (arrow keys keep editing its lines); switching
		// resumes once an entry has been recalled, even when that entry is
		// itself multi-line.
		if message.Code == tea.KeyUp && m.historyBackAllowed() {
			return m.recallHistory(-1), nil, true
		}
		if message.Code == tea.KeyDown && m.historyForwardAllowed() {
			return m.recallHistory(1), nil, true
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
	case m.helpToggleRequested(message):
		m.help.ShowAll = !m.help.ShowAll
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	case key.Matches(message, m.keys.process):
		follow := m.viewport.AtBottom()
		m.toggleProcessGroups()
		m.refreshViewport(follow)
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

func (m model) helpToggleRequested(message tea.KeyPressMsg) bool {
	if !key.Matches(message, m.keys.help) {
		return false
	}

	// Terminals expose committed printable text but not whether it came from
	// an IME. Treat ? as help only when the regular composer is empty; once
	// composition has started, printable text must remain textarea input.
	return m.secretInput == nil && m.input.Value() == ""
}

func (m *model) updateInput(message tea.Msg) tea.Cmd {
	previousValue := m.input.Value()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	if m.input.Value() != previousValue {
		m.commandSelection = 0
		m.commandDismissed = false
		// Editing recalled text turns it back into a fresh draft: arrow keys
		// move the cursor for local changes instead of switching history.
		m.historyIndex = -1
		m.historyDraft = ""
	}
	m.resizeLayout()
	return command
}

func (m model) submit() (model, tea.Cmd, bool) {
	if m.secretInput != nil {
		return m.submitSecretInput()
	}

	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" {
		return m, nil, true
	}
	m.promptHistory = appendPromptHistory(m.promptHistory, prompt)
	m.historyIndex = -1
	m.historyDraft = ""
	if request, slashCommand := parseSlashCommand(prompt); slashCommand {
		return m.submitSlashCommand(prompt, request)
	}
	if m.controllerClosed {
		return m, nil, true
	}

	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: prompt})
	m.beginProcess()
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
		m.processGroups = nil
		m.activeProcessID = 0
		m.resetCommandInput()
		m.status = "Visible transcript cleared; Session history is unchanged"
		m.resizeLayout()
		m.refreshViewport(true)
		// Clearing the transcript brings the welcome screen back, so resume
		// its animated logo.
		return m, m.welcomeAnimation.Start(), true
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
	if command.Menu != nil {
		if request.Arguments != "" {
			return m.commandUsageError(raw, command)
		}
		return m.openCommandMenu(raw, request, command)
	}
	return m.startApplicationSlashCommand(raw, request, command)
}

func (m model) startApplicationSlashCommand(
	raw string,
	request SlashCommandRequest,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	if command.SecretPrompt != "" {
		m.entries = append(
			m.entries,
			transcriptEntry{kind: entryUser, text: raw},
		)
		m.resetCommandInput()
		m.secretInput = &secretInput{
			request: request,
			prompt:  command.SecretPrompt,
		}
		m.input.Placeholder = command.SecretPrompt + " (input hidden)"
		m.status = command.SecretPrompt + " required; Esc cancels"
		m.resizeLayout()
		m.refreshViewport(true)
		return m, m.input.Focus(), true
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

func (m model) openCommandMenu(
	raw string,
	request SlashCommandRequest,
	command SlashCommand,
) (model, tea.Cmd, bool) {
	if command.Menu == nil || len(command.Menu.Options) == 0 {
		return m.commandError(raw, "/"+command.Name+" has no available choices")
	}

	m.commandMenu = &commandMenuState{
		raw:     raw,
		request: request,
		command: command,
		frames: []commandMenuFrame{{
			menu:      *command.Menu,
			selection: currentSlashCommandOption(command.Menu.Options),
		}},
	}
	m.input.Blur()
	m.status = command.Menu.Title + "; Esc cancels"
	m.resizeLayout()
	m.refreshViewport(false)
	return m, nil, true
}

func (m model) selectCommandMenuOption() (model, tea.Cmd, bool) {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return m, nil, true
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	if len(frame.menu.Options) == 0 {
		return m.backOrCancelCommandMenu()
	}
	frame.selection = min(max(frame.selection, 0), len(frame.menu.Options)-1)
	option := frame.menu.Options[frame.selection]
	if option.Menu != nil && len(option.Menu.Options) > 0 {
		m.commandMenu.frames = append(
			m.commandMenu.frames,
			commandMenuFrame{
				menu:      *option.Menu,
				selection: currentSlashCommandOption(option.Menu.Options),
			},
		)
		m.status = option.Menu.Title + "; Esc goes back"
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	}

	state := *m.commandMenu
	state.request.Arguments = option.Arguments
	m.commandMenu = nil
	return m.startApplicationSlashCommand(
		state.raw,
		state.request,
		state.command,
	)
}

func (m model) backOrCancelCommandMenu() (model, tea.Cmd, bool) {
	if m.commandMenu == nil {
		return m, nil, true
	}
	if len(m.commandMenu.frames) > 1 {
		m.commandMenu.frames = m.commandMenu.frames[:len(m.commandMenu.frames)-1]
		frame := m.commandMenu.frames[len(m.commandMenu.frames)-1]
		m.status = frame.menu.Title + "; Esc cancels"
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	}

	name := m.commandMenu.command.Name
	m.commandMenu = nil
	m.resetCommandInput()
	m.status = "/" + name + " selection cancelled"
	m.resizeLayout()
	m.refreshViewport(false)
	return m, m.input.Focus(), true
}

func (m *model) moveCommandMenuSelection(delta int) {
	if m.commandMenu == nil || len(m.commandMenu.frames) == 0 {
		return
	}
	frame := &m.commandMenu.frames[len(m.commandMenu.frames)-1]
	if len(frame.menu.Options) == 0 {
		frame.selection = 0
		return
	}
	frame.selection = (frame.selection + delta + len(frame.menu.Options)) %
		len(frame.menu.Options)
}

func currentSlashCommandOption(options []SlashCommandOption) int {
	for index, option := range options {
		if option.Current {
			return index
		}
	}
	return 0
}

func (m model) submitSecretInput() (model, tea.Cmd, bool) {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		m.status = m.secretInput.prompt + " must not be blank"
		return m, nil, true
	}
	if strings.ContainsAny(value, "\r\n") {
		m.status = m.secretInput.prompt + " must be entered on one line"
		return m, nil, true
	}
	if m.controllerClosed {
		return m.cancelSecretInput()
	}

	request := m.secretInput.request
	request.Secret = value
	m.resetCommandInput()
	m.input.Blur()
	m.running = true
	m.assistantEntry = -1
	m.status = "Saving credential..."
	m.resizeLayout()
	m.refreshViewport(true)
	return m, startSlashCommand(m.requests, m.controllerDone, request), true
}

func (m model) cancelSecretInput() (model, tea.Cmd, bool) {
	prompt := m.secretInput.prompt
	m.resetCommandInput()
	m.entries = append(m.entries, transcriptEntry{
		kind: entryNotice,
		text: prompt + " entry cancelled",
	})
	m.status = prompt + " entry cancelled; run /login to try again"
	m.resizeLayout()
	m.refreshViewport(true)
	return m, m.input.Focus(), true
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
	m.input.Placeholder = defaultPlaceholder
	m.secretInput = nil
	m.commandMenu = nil
	m.commandSelection = 0
	m.commandDismissed = false
	m.historyIndex = -1
	m.historyDraft = ""
}

// historyBackAllowed reports whether Up switches to an earlier prompt instead
// of moving the composer cursor. A single-line composer (including an empty
// one) always switches. Once an entry has been recalled, navigation keeps
// switching even when the recalled entry is multi-line; editing the recalled
// text exits that mode so arrow keys move the cursor for local changes. A
// multi-line draft never switches until the user recalls an entry.
func (m model) historyBackAllowed() bool {
	if m.historyIndex >= 0 {
		return true
	}
	return m.input.LineCount() <= 1 &&
		m.input.Line() == 0 &&
		m.input.LineInfo().RowOffset == 0
}

// historyForwardAllowed reports whether Down moves toward the newest recalled
// prompt. Its single-line rule mirrors historyBackAllowed; pressing Down while
// already on the draft is a harmless no-op handled by recallHistory.
func (m model) historyForwardAllowed() bool {
	if m.historyIndex >= 0 {
		return true
	}
	return m.input.LineCount() <= 1 &&
		m.input.Line() == m.input.LineCount()-1 &&
		m.input.LineInfo().RowOffset+1 == m.input.LineInfo().Height
}

// recallHistory walks promptHistory by delta. Up (-1) moves toward older
// prompts and Down (1) back toward the newest; Down past the newest restores
// the draft that was in the composer when recall started.
func (m model) recallHistory(delta int) model {
	if len(m.promptHistory) == 0 {
		return m
	}
	if m.historyIndex < 0 {
		if delta < 0 {
			m.historyDraft = m.input.Value()
			m.historyIndex = len(m.promptHistory) - 1
		} else {
			return m
		}
	} else {
		m.historyIndex += delta
		if m.historyIndex >= len(m.promptHistory) {
			m.historyIndex = -1
			m.input.SetValue(m.historyDraft)
			m.input.CursorEnd()
			m.resizeLayout()
			return m
		}
		m.historyIndex = max(m.historyIndex, 0)
	}
	m.input.SetValue(m.promptHistory[m.historyIndex])
	m.input.CursorEnd()
	m.resizeLayout()
	return m
}

// appendPromptHistory records a submitted prompt, dropping consecutive
// duplicates and keeping at most maximumPromptHistory entries.
func appendPromptHistory(history []string, prompt string) []string {
	if prompt == "" {
		return history
	}
	if count := len(history); count > 0 && history[count-1] == prompt {
		return history
	}
	if len(history) >= maximumPromptHistory {
		history = append(history[:0], history[1:]...)
	}
	return append(history, prompt)
}
