package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	sourceID            string
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
	// toolEvidence is an immutable projection assigned once on completion; the
	// pointer keeps transcriptEntry comparable for viewport cache versions.
	toolEvidence *interaction.EvidenceDisplay
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
	draft     string
}

type commandMenuState struct {
	raw     string
	request SlashCommandRequest
	command SlashCommand
	frames  []commandMenuFrame
}

type model struct {
	reading                  *sessionReading
	readSession              func(uint64, string, string) (tea.Cmd, context.CancelFunc)
	renameSession            func(uint64, string, string) (tea.Cmd, context.CancelFunc)
	sessionID                string
	sessionPicker            *sessionPicker
	sessionQueryGeneration   uint64
	sessionPreviewGeneration uint64
	searchSessions           func(uint64, string) (tea.Cmd, context.CancelFunc)
	previewSession           func(uint64, string, string) (tea.Cmd, context.CancelFunc)
	completeFiles            func(uint64, string) (tea.Cmd, context.CancelFunc)
	fileCompletion           fileCompletionState
	requests                 chan<- runRequest
	controllerDone           <-chan struct{}
	sideRequests             chan<- runRequest
	sideControllerDone       <-chan struct{}
	updates                  <-chan runUpdate
	prepareDelivery          func(ActiveRun, interaction.Delivery, composerDraft) (tea.Cmd, context.CancelFunc)
	deliveryPending          bool
	cancelDelivery           context.CancelFunc
	activeRun                ActiveRun
	cancelRun                context.CancelFunc
	side                     sidePanelState
	guardRequests            <-chan interaction.GuardRequest
	guardPending             *interaction.GuardRequest
	guardViewport            viewport.Model
	guardSelection           int
	guardFeedback            bool
	guardFeedbackText        string

	viewport              transcriptViewport
	selection             transcriptSelection
	folds                 map[foldTarget]bool
	pointer               transcriptPointer
	composerActive        bool
	input                 composerInput
	spinner               spinner.Model
	help                  help.Model
	keys                  keyMap
	currentModel          DisplayModel
	thinking              DisplayThinking
	apiKeyConfigured      bool
	sessionUsage          DisplayUsage
	contextShowFraction   bool
	contextPressed        bool
	workspacePress        *tea.Mouse
	openDirectory         func(string) tea.Cmd
	contextHoverConfirmed bool
	contextUsage          DisplayContext
	usageAnimation        usageAnimation
	welcomeAnimation      welcomeAnimation
	welcomeTip            welcomeTip
	updateCheck           tea.Cmd
	welcomeUpdate         welcomeUpdateStatus
	workingDirectory      string
	version               string
	entries               []transcriptEntry
	processGroups         []processGroup
	commands              []SlashCommand
	authInput             chan string
	authPrompt            *interaction.AuthPrompt
	authSelection         int
	authCommand           string
	secretInput           *secretInput
	commandMenu           *commandMenuState
	customLogin           *customLoginState
	pendingDeliveries     []pendingDelivery

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

	inputGeneration  uint64
	chrome           chromeMeasurements
	capture          pointerCapture
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
	// Selection edits bypass the composer's atomic attachment boundaries.
	// Keep these opt-in until selections can preserve those spans; Ctrl+G
	// remains AICE's external-editor shortcut.
	for _, binding := range []*key.Binding{
		&input.KeyMap.SelectCharacterForward, &input.KeyMap.SelectCharacterBackward,
		&input.KeyMap.SelectWordForward, &input.KeyMap.SelectWordBackward,
		&input.KeyMap.SelectLineUp, &input.KeyMap.SelectLineDown,
		&input.KeyMap.SelectAll, &input.KeyMap.CopySelection,
	} {
		binding.SetEnabled(false)
	}
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
		spinner.WithStyle(infoStyle),
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
		input:          composerInput{Model: input},
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
		chrome: chromeMeasurements{header: 2, composer: 3, footer: 1},
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
	before := m.inputIdentity()
	beforeHelp := m.expandedHelpLayout()
	m.beginInputEvent(message)
	next, command := m.update(message)
	updated := next.(model)
	transition := updated.finishInputTransition(before)
	if beforeHelp != updated.expandedHelpLayout() {
		updated.resizeLayout()
		updated.refreshViewport(false)
	}
	updated.finishPointerEvent(message)
	return updated, tea.Batch(command, transition)
}

func (m model) update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyPressMsg:
		return m.routeKey(message)
	case tea.PasteMsg:
		return m.routePaste(message)
	case tea.KeyReleaseMsg:
		return m, nil
	}
	switch message := message.(type) {
	case inputComponentResult:
		if message.generation != m.inputGeneration || message.owner != m.inputIdentity() {
			return m, nil
		}
		if m.sessionPicker != nil {
			return m.handleSessionPicker(message.message)
		}
		if m.composerInputEnabled() {
			command := m.updateInput(message.message)
			return m, command
		}
		return m, nil
	case sessionRenameResult:
		return m.applySessionRename(message)
	case sessionReadingResult:
		return m.applySessionReading(message)
	case sessionSearchResult:
		return m.applySessionSearch(message)
	case sessionPreviewResult:
		return m.applySessionPreview(message)
	case directoryOpenedMsg:
		if message.err != nil {
			m.inputNotice = message.err.Error()
			m.resizeLayout()
			m.refreshViewport(false)
		}
		return m, nil
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
		m.workspacePress = nil
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
		m.workspacePress = nil
		m.contextPressed = false
		m.selection.clear()
		m.width = message.Width
		m.height = message.Height
		m.resizeSessionPicker()
		m.resizeLayout()
		m.refreshViewport(false)
		if m.reading != nil && m.reading.directory {
			m.showTurnDirectory()
		}
		return m, nil
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.BlurMsg:
		return m.routePointer(message)
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
		if command != nil && m.welcomeTipsVisible() && m.guardPending == nil {
			m.welcomeTip.advance(message.at)
		} else if message.generation == m.welcomeAnimation.generation {
			m.welcomeTip.nextAt = time.Time{}
		}
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

	// Component clocks are routed explicitly. Private paste replies arrive in
	// inputComponentResult; unrelated messages never rebuild editor layout.
	if _, ok := message.(cursor.BlinkMsg); ok && m.sessionPicker != nil {
		return m.handleSessionPicker(message)
	}
	return m, nil
}

func (m model) View() tea.View {
	if m.guardPending != nil {
		return m.terminalView(m.guardView(max(m.width-2*m.horizontalPadding(), 1)))
	}
	width := m.layoutWidth()
	viewportView := m.viewport.viewWithCodeHover(m.hoveredFold(), m.hoveredCode())
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

	return m.terminalView(content)
}

func (m model) terminalView(content string) tea.View {
	// Paint the canvas, including empty cells, instead of changing the host's
	// default colors with OSC 10/11. Terminal-owned overlays such as IME
	// composition must not share mutable palette state with the app theme.
	content = lipgloss.NewStyle().
		Foreground(primaryTextColor).
		Background(inkBlackColor).
		Width(m.width).
		Height(m.height).
		Padding(m.verticalPadding(), m.horizontalPadding()).
		Render(content)
	// Nested styles reset SGR without restoring their parent's colors. Reapply
	// the canvas defaults after resets; explicit panel/selection colors follow
	// those resets and still take precedence.
	content = restoreCanvasColors(content)
	// Resolve canvas colors before composition: the compositor merges SGR
	// resets with other attributes, so textual reset restoration must run first.
	content = m.overlayCopyNotice(content, m.width)
	content, pickerCursor := m.overlaySessionPicker(content)
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "AICE"
	view.MouseMode = tea.MouseModeAllMotion
	view.ReportFocus = true
	if m.sessionPicker != nil {
		view.Cursor = pickerCursor
	} else if m.secretInput == nil && m.authInput == nil && m.guardPending == nil {
		// Anchor the real terminal cursor on the composer caret. The IME
		// candidate window follows the terminal cursor, and Bubble Tea's
		// renderer hides the cursor around every updated frame and restores
		// it here, so repaints (such as the welcome logo sweep) no longer
		// drag the input method away from the input field. Guard confirmation
		// replaces the composer, so its caret must not keep leaking through.
		if cursor := m.input.Cursor(); cursor != nil {
			m.positionComposerCursor(&cursor.Position, m.layoutWidth())
			view.Cursor = cursor
		}
	}
	return view
}

// Resolve defaults before layers become cells; nested styled spans can reset
// their parent background as well as foreground.
func restoreCanvasColors(content string) string {
	colors := ansi.Style{}.ForegroundColor(primaryTextColor).BackgroundColor(inkBlackColor).String()
	restore := strings.NewReplacer(
		"\x1b[m", "\x1b[m"+colors,
		"\x1b[0m", "\x1b[0m"+colors,
		"\x1b[39m", ansi.Style{}.ForegroundColor(primaryTextColor).String(),
		"\x1b[49m", ansi.Style{}.BackgroundColor(inkBlackColor).String(),
	)
	return colors + restore.Replace(content) + "\x1b[0m"
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
	position.X += m.horizontalPadding() + style.GetMarginLeft() +
		style.GetPaddingLeft() +
		style.GetBorderLeftSize()
	position.Y += top +
		style.GetMarginTop() +
		style.GetPaddingTop() +
		style.GetBorderTopSize()
	position.Y += m.screenLayout().composer.y
}

// clearInputOrQuit consumes the first press even when the editor is empty.
// Only a consecutive press on an empty editor exits; shutdown belongs to Run.
func (m model) clearInputOrQuit() (model, tea.Cmd, bool) {
	if m.clearQuitPending && m.input.Value() == "" && len(m.pastes) == 0 {
		return m, tea.Quit, true
	}
	m.inputGeneration++
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

func (m *model) updateInput(message tea.Msg) tea.Cmd {
	if _, pasted := message.(tea.PasteMsg); pasted {
		m.clearQuitPending = false
		m.composerActive = true
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
		return m.scopeInputCommand(command)
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
			m.syncCommandCompletion()
			m.resizeLayout()
			return nil
		}
	}
	previousValue := m.input.Value()
	previousRow, previousCol := m.input.Line(), m.input.Column()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	command = m.scopeInputCommand(command)
	nextValue := m.input.Value()
	if nextValue != previousValue {
		// Clipboard pastes and other large inserts that bypass PasteMsg
		// still collapse in place, keeping surrounding text in order.
		if before, added, after := splitInputChange(previousValue, nextValue); m.secretInput == nil &&
			exceedsPasteThreshold(added) {
			// The pre-collapse viewport sync no longer matches the
			// shortened content; the collapse re-syncs below.
			command = m.collapseLargeInsert(before, added, after)
		}
		m.dropOrphanPasteAttachments()
		m.commandSelection = 0
		m.commandDismissed = false
		// Editing recalled text turns it back into a fresh draft: arrow keys
		// move the cursor for local changes instead of switching history.
		m.historyIndex = -1
		m.historyDraft = ""
	}
	m.snapCursorOutOfPasteToken(previousRow, previousCol)
	m.syncCommandCompletion()
	if nextValue != previousValue && m.commandMenu != nil {
		m.resetCommandOptionSelection()
	}
	completion := m.requestFileCompletion()
	m.resizeLayout()
	return tea.Batch(command, completion)
}
