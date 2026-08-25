package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
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
	startedAt time.Time
	elapsed   time.Duration
}

type transcriptViewPart struct {
	content string
	tool    bool
}

type secretInput struct {
	request SlashCommandRequest
	prompt  string
}

type customLoginState struct {
	endpoint string
	apiKey   string
	step     int // 0: endpoint, 1: api key, 2: model
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
	requests           chan<- runRequest
	controllerDone     <-chan struct{}
	sideRequests       chan<- runRequest
	sideControllerDone <-chan struct{}
	updates            <-chan runUpdate
	activeRun          ActiveRun
	cancelRun          context.CancelFunc
	side               sidePanelState
	guardRequests      <-chan interaction.GuardRequest
	guardPending       *interaction.GuardRequest
	guardSelection     int
	guardFeedback      bool
	guardFeedbackText  string

	viewport          viewport.Model
	selection         transcriptSelection
	input             textarea.Model
	spinner           spinner.Model
	help              help.Model
	keys              keyMap
	currentModel      DisplayModel
	thinking          DisplayThinking
	apiKeyConfigured  bool
	sessionUsage      DisplayUsage
	usageAnimation    usageAnimation
	welcomeAnimation  welcomeAnimation
	updateCheck       tea.Cmd
	welcomeUpdate     welcomeUpdateStatus
	workingDirectory  string
	version           string
	entries           []transcriptEntry
	processGroups     []processGroup
	commands          []SlashCommand
	secretInput       *secretInput
	commandMenu       *commandMenuState
	customLogin       *customLoginState
	pendingDeliveries []pendingDelivery

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
	acceptsDelivery  bool
	cancelRequested  bool
	controllerClosed bool
	status           string
	nextDeliveryID   uint64
	steerRailFrame   uint8
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
	// Render the caret with the real terminal cursor instead of a drawn one.
	// The real cursor is positioned by View() via tea.View.Cursor, and it is
	// what anchors the IME candidate window to the composer.
	input.SetVirtualCursor(false)

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
		side: sidePanelState{
			threads: map[uint64]*sideThreadState{},
		},
		status: "Ready",
		// The welcome animation starts running at construction; Init() emits
		// its first tick. It pauses once the run starts and resumes on /clear.
		welcomeAnimation: welcomeAnimation{running: true, generation: 1},
	}
}

func (m model) Init() tea.Cmd {
	commands := []tea.Cmd{textarea.Blink, m.welcomeAnimation.tick()}
	if m.updateCheck != nil {
		commands = append(commands, m.updateCheck)
	}
	if m.guardRequests != nil {
		commands = append(commands, waitForGuardRequest(m.guardRequests))
	}
	return tea.Batch(commands...)
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case guardRequestMsg:
		if message.req != nil {
			m.guardPending = message.req
			m.guardSelection = 0
			m.guardFeedback = false
			m.guardFeedbackText = ""
			m.input.Blur()
			m.resizeLayout()
			m.refreshViewport(true)
			return m, nil
		}
		return m, nil
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
		if m.composerInputEnabled() {
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
	case sideRunStartedMsg:
		if message.isNew {
			if pending := m.side.newPending; pending != nil &&
				pending.question == message.question {
				pending.ch = message.updates
			}
		} else if thread := m.side.thread(message.threadID); thread != nil &&
			thread.isRunning && thread.updates == nil {
			thread.updates = message.updates
		}
		return m, tea.Batch(
			waitForSideRunUpdates(message.updates),
			m.spinner.Tick,
		)
	case sideRunUnavailableMsg:
		m.side.closed = true
		if message.isNew {
			m.side.newPending = nil
			m.side.newDraft = message.question
		} else if thread := m.side.thread(message.threadID); thread != nil {
			thread.isRunning = false
			thread.updates = nil
			thread.draft = message.question
		}
		m.side.notice = "The BTW side-thread controller stopped"
		if m.side.isVisible {
			m.input.SetValue(message.question)
			m.input.CursorEnd()
			if m.composerInputEnabled() {
				m.input.Focus()
			} else {
				m.input.Blur()
			}
		}
		m.resizeLayout()
		m.refreshViewport(true)
		return m, nil
	case sideRunBatchMsg:
		return m.applySideRunBatch(message)
	case updateCheckMsg:
		m.updateCheck = nil
		m.welcomeUpdate = welcomeUpdateStatus{latest: message.result.Latest}
		if message.err != nil {
			m.welcomeUpdate.state = welcomeUpdateFailed
		} else {
			switch message.result.Status {
			case UpdateCheckStatusDisabled:
				m.welcomeUpdate.state = welcomeUpdateDisabled
			case UpdateCheckStatusDevelopment:
				m.welcomeUpdate.state = welcomeUpdateDevelopment
			case UpdateCheckStatusCurrent:
				m.welcomeUpdate.state = welcomeUpdateCurrent
			case UpdateCheckStatusAvailable:
				m.welcomeUpdate.state = welcomeUpdateAvailable
			default:
				m.welcomeUpdate.state = welcomeUpdateFailed
			}
		}
		m.refreshViewport(false)
		return m, nil
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
		if m.running || m.side.anyRunning() {
			hasPendingSteer := m.hasPendingSteer()
			if hasPendingSteer {
				m.steerRailFrame = (m.steerRailFrame + 1) % 4
			}
			durationChanged := m.updateActiveProcessDuration(message.Time)
			refreshMain := m.running &&
				(m.showsActivitySpinner() || durationChanged || hasPendingSteer)
			refreshSide := false
			if m.side.isVisible {
				if thread := m.side.activeThread(); thread != nil && thread.isRunning {
					refreshSide = true
				}
			}
			if refreshMain || refreshSide {
				m.refreshViewport(false)
			}
			return m, command
		}
		return m, nil
	}

	var commands []tea.Cmd
	if m.running || m.side.anyRunning() {
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		commands = append(commands, command)
	}
	if m.composerInputEnabled() {
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
	if m.secretInput == nil && m.guardPending == nil {
		// Anchor the real terminal cursor on the composer caret. The IME
		// candidate window follows the terminal cursor, and Bubble Tea's
		// renderer hides the cursor around every updated frame and restores
		// it here, so repaints (such as the welcome logo sweep) no longer
		// drag the input method away from the input field. Guard confirmation
		// replaces the composer, so its caret must not keep leaking through.
		if cursor := m.input.Cursor(); cursor != nil {
			m.positionComposerCursor(&cursor.Position, width)
			view.Cursor = cursor
		}
	}
	return view
}

// positionComposerCursor translates the input field's internal cursor
// position into screen coordinates. The composer frame is bottom-aligned
// (header, transcript, command menu, composer, footer) and may carry a
// pending-queue notice above the input field, both of which offset the caret.
func (m model) positionComposerCursor(position *tea.Position, width int) {
	style := composerFocusedStyle
	if !m.input.Focused() {
		style = composerBlurredStyle
	}
	contentWidth := max(width-style.GetHorizontalFrameSize(), 1)
	parts := m.composerParts(contentWidth)
	// The input field is the last part; earlier parts sit above it.
	top := 0
	for index := 0; index < len(parts)-1; index++ {
		top += lipgloss.Height(parts[index]) + 1
	}
	position.X += style.GetMarginLeft() +
		style.GetPaddingLeft() +
		style.GetBorderLeftSize()
	position.Y += top +
		style.GetMarginTop() +
		style.GetPaddingTop() +
		style.GetBorderTopSize()
	position.Y += m.height -
		lipgloss.Height(m.composerView(width)) -
		lipgloss.Height(m.footerView(width))
}

func (m model) handleKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if m.guardPending != nil {
		updated, cmd, handled := m.handleGuardKey(message)
		if handled {
			return updated, cmd, true
		}
		return m, nil, true
	}
	if m.side.menu != nil {
		return m.handleSideMenuKey(message)
	}
	if m.side.confirm != nil {
		return m.handleSideConfirmKey(message)
	}
	if m.side.isVisible {
		if updated, command, handled := m.handleSideKey(message); handled {
			return updated, command, true
		}
		// The side panel owns all keyboard input while visible. Unhandled keys
		// fall through to its textarea in Update, never to main-run shortcuts or
		// prompt-history navigation.
		return m, nil, false
	}

	if !m.running && m.secretInput != nil {
		switch {
		case cancelKeyPressed(message, m.keys):
			return m.cancelSecretInput()
		case key.Matches(message, m.keys.newline):
			m.status = "API key must be entered on one line"
			return m, nil, true
		}
	}

	if !m.running && m.commandMenu != nil {
		switch {
		case cancelKeyPressed(message, m.keys):
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
	case key.Matches(message, m.keys.queue):
		if m.running {
			if m.isBTWCommandInput() {
				return m.submit()
			}
			if m.acceptsDelivery {
				return m.submitDelivery(deliveryQueue)
			}
			m.status = "Current command is still running"
			return m, nil, true
		}
		return m, nil, true
	case key.Matches(message, m.keys.newline):
		if m.composerInputEnabled() {
			m.input.InsertString("\n")
			m.resizeLayout()
		}
		return m, nil, true
	case key.Matches(message, m.keys.send):
		if m.running {
			if m.isBTWCommandInput() {
				return m.submit()
			}
			if m.acceptsDelivery {
				return m.submitDelivery(deliverySteer)
			}
			m.status = "Current command is still running"
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

func (m model) composerInputEnabled() bool {
	if m.guardPending != nil {
		return false
	}
	if m.side.isVisible {
		if m.side.activeID == 0 {
			return m.side.newPending == nil
		}
		thread := m.side.activeThread()
		return thread != nil && !thread.isRunning && !thread.readOnly()
	}
	if m.side.menu != nil {
		return false
	}
	return !m.running || m.acceptsDelivery
}

func cancelKeyPressed(message tea.KeyPressMsg, keys keyMap) bool {
	return message.Code == tea.KeyEscape ||
		key.Matches(message, keys.interrupt) ||
		key.Matches(message, keys.quit)
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
