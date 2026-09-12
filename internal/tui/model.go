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
	minimumWidth       = 24
	minimumViewport    = 1
	maximumEventBatch  = 64
	inputMaximumHeight = 6
	// inputMaximumContentHeight caps composer content in visual rows.
	// MaxHeight only limits the visible viewport (6 rows with scrolling);
	// MaxContentHeight is the content gate that silently truncates pastes.
	// It must stay huge (not 0): 0 falls back to the legacy MaxHeight
	// logical-line guard and blocks Enter after 6 lines. 10000 matches the
	// upstream textarea maxLines order so normal pastes are effectively
	// unlimited while pathological input stays bounded.
	inputMaximumContentHeight = 10000
	maximumCommandRows        = 6
	maximumPromptHistory      = 100
	defaultPlaceholder        = "Ask about this workspace..."
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
	kind                entryKind
	text                string
	thinking            string
	presentation        *assistantPresentation
	complete            bool
	processID           int
	conclusion          bool
	toolPreviewRevision uint64
	writePreview        *writePreview
	toolIndex           int
	toolAssistant       int
	toolPreparing       bool
	toolExpanded        bool
	toolOutput          interaction.ToolOutputDisplay
	toolID              string
	toolName            string
	toolDetail          string
	toolDone            bool
	toolError           bool
	toolTruncation      interaction.TruncationDisplay
	toolDiff            interaction.DiffDisplay
}

type processGroup struct {
	id        int
	collapsed bool
	manual    bool
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
	completeFiles      func(uint64, string) (tea.Cmd, context.CancelFunc)
	fileCompletion     fileCompletionState
	requests           chan<- runRequest
	controllerDone     <-chan struct{}
	sideRequests       chan<- runRequest
	sideControllerDone <-chan struct{}
	updates            <-chan runUpdate
	prepareDelivery    func(ActiveRun, interaction.Delivery, composerDraft) (tea.Cmd, context.CancelFunc)
	deliveryPending    bool
	cancelDelivery     context.CancelFunc
	activeRun          ActiveRun
	cancelRun          context.CancelFunc
	side               sidePanelState
	guardRequests      <-chan interaction.GuardRequest
	guardPending       *interaction.GuardRequest
	guardViewport      viewport.Model
	guardSelection     int
	guardFeedback      bool
	guardFeedbackText  string

	viewport          transcriptViewport
	selection         transcriptSelection
	folds             map[foldTarget]bool
	pointer           transcriptPointer
	input             textarea.Model
	spinner           spinner.Model
	help              help.Model
	keys              keyMap
	currentModel      DisplayModel
	thinking          DisplayThinking
	apiKeyConfigured  bool
	sessionUsage      DisplayUsage
	contextUsage      DisplayContext
	usageAnimation    usageAnimation
	welcomeAnimation  welcomeAnimation
	updateCheck       tea.Cmd
	welcomeUpdate     welcomeUpdateStatus
	workingDirectory  string
	version           string
	entries           []transcriptEntry
	processGroups     []processGroup
	commands          []SlashCommand
	authInput         chan string
	authPrompt        *interaction.AuthPrompt
	authSelection     int
	authCommand       string
	secretInput       *secretInput
	commandMenu       *commandMenuState
	customLogin       *customLoginState
	pendingDeliveries []pendingDelivery

	promptHistory []string
	historyIndex  int
	historyDraft  string
	// pastes owns text and image payloads behind inline placeholder tokens.
	pastes           []pasteAttachment
	clipboard        tea.Cmd
	clipboardPending bool
	clipboardDiscard bool
	clearQuitPending bool
	clipboardInSide  bool
	clipboardSideID  uint64
	inputNotice      string
	// Retained only until NewRun accepts the submission, so rejection restores it.
	submittedInput *RunInput
	submittedDraft composerDraft

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
	copyNotice       bool
	copyGeneration   uint64
	nextDeliveryID   uint64
	steerRailFrame   uint8
}

func newModel(
	requests chan<- runRequest,
	controllerDone <-chan struct{},
	externalCommands ...SlashCommand,
) model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = defaultPlaceholder
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = inputMaximumHeight
	input.MaxContentHeight = inputMaximumContentHeight
	input.SetHeight(1)
	input.SetWidth(80)
	inputStyles := textarea.DefaultDarkStyles()
	inputStyles.Focused.Base = bodyStyle
	inputStyles.Focused.Text = bodyStyle
	inputStyles.Focused.CursorLine = bodyStyle
	inputStyles.Focused.CursorLineNumber = mutedStyle
	inputStyles.Focused.EndOfBuffer = mutedStyle
	inputStyles.Focused.LineNumber = mutedStyle
	inputStyles.Focused.Placeholder = mutedStyle
	inputStyles.Blurred.Base = bodyStyle
	inputStyles.Blurred.Text = bodyStyle
	inputStyles.Blurred.CursorLine = bodyStyle
	inputStyles.Blurred.CursorLineNumber = mutedStyle
	inputStyles.Blurred.EndOfBuffer = mutedStyle
	inputStyles.Blurred.LineNumber = mutedStyle
	inputStyles.Blurred.Placeholder = mutedStyle
	inputStyles.Cursor.Color = secondaryColor
	input.SetStyles(inputStyles)
	input.Focus()
	// Render the caret with the real terminal cursor instead of a drawn one.
	// The real cursor is positioned by View() via tea.View.Cursor, and it is
	// what anchors the IME candidate window to the composer.
	input.SetVirtualCursor(false)

	view := newTranscriptViewport()

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
	case clipboardResult:
		m.clipboardPending = false
		if m.clipboardDiscard {
			m.clipboardDiscard = false
			return m, nil
		}
		if m.side.isVisible != m.clipboardInSide || m.side.activeID != m.clipboardSideID {
			return m, nil
		}
		return m.applyClipboard(message)
	case copyNoticeExpiredMsg:
		if uint64(message) == m.copyGeneration {
			m.copyNotice = false
		}
		return m, nil
	case guardExpiredMsg:
		if m.guardPending != nil && m.guardPending.Reply == message.reply {
			m.sendGuardReply("", "")
			return m, m.nextGuardWait()
		}
		return m, nil
	case guardRequestMsg:
		if message.req != nil {
			select {
			case <-message.req.Done:
				return m, m.nextGuardWait()
			default:
			}
			m.guardPending = message.req
			m.selection.clear()
			m.guardViewport = viewport.New()
			m.guardViewport.KeyMap = viewport.KeyMap{}
			m.guardViewport.FillHeight = true
			m.guardSelection = 0
			m.guardFeedback = false
			m.guardFeedbackText = ""
			m.input.Blur()
			m.resizeLayout()
			m.refreshViewport(true)
			return m, waitForGuardExpiry(message.req)
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.selection.clear()
		m.width = message.Width
		m.height = message.Height
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil
	case tea.KeyPressMsg:
		m.selection.clear()
		updated, command, handled := m.handleKey(message)
		m = updated
		if handled {
			return m, command
		}
		if m.composerInputEnabled() {
			command := m.updateInput(message)
			return m, command
		}
	case tea.MouseClickMsg:
		m.trackPointer(message.Mouse())
		if m.guardPending != nil {
			return m, nil
		}
		if updated, command, handled := m.handleTranscriptMouseClick(message); handled {
			return updated, command
		}
	case tea.MouseMotionMsg:
		m.trackPointer(message.Mouse())
		if m.guardPending != nil {
			return m, nil
		}
		if updated, command, handled := m.handleTranscriptMouseMotion(message); handled {
			return updated, command
		}
		return m, nil
	case tea.MouseReleaseMsg:
		m.trackPointer(message.Mouse())
		if m.guardPending != nil {
			return m, nil
		}
		if updated, command, handled := m.handleTranscriptMouseRelease(message); handled {
			return updated, command
		}
	case tea.MouseWheelMsg:
		m.trackPointer(message.Mouse())
		m.selection.clear()
		if m.guardPending != nil {
			var command tea.Cmd
			m.guardViewport, command = m.guardViewport.Update(message)
			return m, command
		}
	case tea.BlurMsg:
		m.pointer = transcriptPointer{}
		m.selection.clear()
		return m, nil
	case editorFinishedMsg:
		m = m.applyEditorResult(message)
		m.refreshViewport(false)
		return m, nil
	case fileCompletionResult:
		if message.generation == m.fileCompletion.generation {
			m.fileCompletion.pending = false
			m.fileCompletion.items = message.items
			if message.err != nil {
				m.fileCompletion.items = nil
			}
			m.fileCompletion.selection = 0
			m.resizeLayout()
			m.refreshViewport(false)
		}
		return m, nil
	case deliveryResult:
		return m.applyDeliveryResult(message)
	case runStartedMsg:
		m.updates = message.updates
		return m, tea.Batch(waitForRunUpdates(message.updates), m.spinner.Tick)
	case runUnavailableMsg:
		m.controllerClosed = true
		m.restoreSubmittedInput()
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
	if m.guardPending != nil {
		return m.terminalView(m.guardView(max(m.width, 1)))
	}
	width := max(m.width, minimumWidth)
	viewportView := m.viewport.viewWithHover(m.hoveredFold())
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

	content = m.overlayCopyNotice(content, width)
	return m.terminalView(content)
}

func (m model) terminalView(content string) tea.View {
	view := tea.NewView(content)
	view.BackgroundColor = inkBlackColor
	view.ForegroundColor = primaryTextColor
	view.AltScreen = true
	view.WindowTitle = "AICE"
	view.MouseMode = tea.MouseModeAllMotion
	view.ReportFocus = true
	if m.secretInput == nil && m.authInput == nil && m.guardPending == nil {
		// Anchor the real terminal cursor on the composer caret. The IME
		// candidate window follows the terminal cursor, and Bubble Tea's
		// renderer hides the cursor around every updated frame and restores
		// it here, so repaints (such as the welcome logo sweep) no longer
		// drag the input method away from the input field. Guard confirmation
		// replaces the composer, so its caret must not keep leaking through.
		if cursor := m.input.Cursor(); cursor != nil {
			m.positionComposerCursor(&cursor.Position, max(m.width, minimumWidth))
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
		// Height already counts the row terminated by the join separator.
		top += lipgloss.Height(parts[index])
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
	if m.clearQuitPending && (!key.Matches(message, m.keys.clear) ||
		m.guardPending != nil || m.authInput != nil || m.secretInput != nil ||
		m.commandMenu != nil || m.side.menu != nil || m.side.confirm != nil) {
		m.clearQuitPending = false
		m.resizeLayout()
	}
	if m.guardPending != nil {
		updated, cmd, handled := m.handleGuardKey(message)
		if handled {
			return updated, cmd, true
		}
		return m, nil, true
	}
	if m.authInput != nil {
		return m.handleAuthKey(message)
	}
	if m.side.menu != nil {
		return m.handleSideMenuKey(message)
	}
	if m.side.confirm != nil {
		return m.handleSideConfirmKey(message)
	}
	if m.deliveryPending {
		if cancelKeyPressed(message, m.keys) && m.cancelDelivery != nil {
			m.cancelDelivery()
		}
		return m, nil, true
	}
	if m.clipboardPending && (key.Matches(message, m.keys.paste) ||
		key.Matches(message, m.keys.send) || key.Matches(message, m.keys.queue) ||
		key.Matches(message, m.keys.editor)) {
		m.inputNotice = "Reading clipboard; press Enter again when the attachment appears"
		if m.side.isVisible {
			m.side.notice = "Reading clipboard; press Enter again when ready"
		}
		return m.settleCommand(false, nil)
	}
	if m.composerInputEnabled() && m.secretInput == nil && m.commandMenu == nil {
		if key.Matches(message, m.keys.paste) && m.clipboard != nil {
			m.clipboardPending = true
			m.clipboardInSide = m.side.isVisible
			m.clipboardSideID = m.side.activeID
			return m, m.clipboard, true
		}
	}
	if m.secretInput == nil && m.commandMenu == nil && key.Matches(message, m.keys.clear) {
		return m.clearInputOrQuit()
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

	if m.secretInput == nil &&
		m.commandMenu == nil &&
		m.composerInputEnabled() {
		// Ctrl+G edits the composer in the default editor; placeholder
		// tokens stay atomic for cursor motion and deletion.
		if !m.running && key.Matches(message, m.keys.editor) {
			updated, command := m.openComposerEditor()
			return updated, command, true
		}
		if updated, command, handled := m.handlePasteTokenKey(message); handled {
			updated.resizeLayout()
			return updated, command, true
		}
	}

	if updated, command, handled := m.handleFileCompletionKey(message); handled {
		return updated, command, true
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
		return m, nil, true
	case key.Matches(message, m.keys.quit):
		if !m.running && strings.TrimSpace(m.expandComposerText()) == "" && len(m.composerImages()) == 0 {
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

// clearInputOrQuit consumes the first press even when the editor is empty.
// Only a consecutive press on an empty editor exits; shutdown belongs to Run.
func (m model) clearInputOrQuit() (model, tea.Cmd, bool) {
	if m.clearQuitPending && m.input.Value() == "" && len(m.pastes) == 0 {
		return m, tea.Quit, true
	}
	m.input.Reset()
	m.pastes = nil
	m.inputNotice = ""
	m.historyIndex = -1
	m.historyDraft = ""
	m.commandSelection = 0
	m.commandDismissed = false
	// A clipboard helper already in flight must not refill the cleared draft.
	m.clipboardDiscard = m.clipboardPending
	m.clearQuitPending = true
	if m.side.isVisible {
		if thread := m.side.activeThread(); thread != nil {
			thread.draft = ""
		} else {
			m.side.newDraft = ""
		}
	}
	return m.settleCommand(false, nil)
}

func (m model) composerInputEnabled() bool {
	if m.deliveryPending {
		return false
	}
	if m.authInput != nil {
		return m.authPrompt != nil && m.authPrompt.AllowInput && !m.cancelRequested
	}
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
		key.Matches(message, keys.clear) ||
		key.Matches(message, keys.quit)
}

func (m model) helpToggleRequested(message tea.KeyPressMsg) bool {
	if !key.Matches(message, m.keys.help) {
		return false
	}

	// Terminals expose committed printable text but not whether it came from
	// an IME. Treat ? as help only when the regular composer is empty; once
	// composition has started, printable text must remain textarea input.
	return m.secretInput == nil && strings.TrimSpace(m.expandComposerText()) == "" && len(m.composerImages()) == 0
}

func (m *model) updateInput(message tea.Msg) tea.Cmd {
	if _, pasted := message.(tea.PasteMsg); pasted {
		m.clearQuitPending = false
	}
	if m.authInput != nil {
		if paste, ok := message.(tea.PasteMsg); ok {
			value := strings.TrimSpace(paste.Content)
			if len(value)+len(m.input.Value()) <= 65536 {
				m.input.InsertString(value)
			}
			return nil
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		return command
	}

	// A bracketed paste arrives whole: collapse it before the textarea can
	// truncate it against the content-height gate, so no pasted line is lost.
	if paste, ok := message.(tea.PasteMsg); ok && m.secretInput == nil {
		if exceedsPasteThreshold(paste.Content) {
			m.insertPastePlaceholder(paste.Content)
			m.commandSelection = 0
			m.commandDismissed = false
			m.historyIndex = -1
			m.historyDraft = ""
			m.resizeLayout()
			return nil
		}
	}
	previousValue := m.input.Value()
	previousRow, previousCol := m.input.Line(), m.input.Column()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	nextValue := m.input.Value()
	if nextValue != previousValue {
		// Clipboard pastes and other large inserts that bypass PasteMsg
		// still collapse in place, keeping surrounding text in order.
		if before, added, after := splitInputChange(previousValue, nextValue); m.secretInput == nil &&
			exceedsPasteThreshold(added) {
			// The pre-collapse viewport sync no longer matches the
			// shortened content; the collapse re-syncs below.
			command = m.collapseLargeInsert(before, added, after)
		} else {
			m.snapCursorOutOfPasteToken(previousRow, previousCol)
		}
		m.dropOrphanPasteAttachments()
		m.commandSelection = 0
		m.commandDismissed = false
		// Editing recalled text turns it back into a fresh draft: arrow keys
		// move the cursor for local changes instead of switching history.
		m.historyIndex = -1
		m.historyDraft = ""
	}
	completion := m.requestFileCompletion()
	m.resizeLayout()
	return tea.Batch(command, completion)
}
