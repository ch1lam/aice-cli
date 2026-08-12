package tui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	maximumSideEntries = 20
	sidePlaceholder    = "Ask a side question about this Session..."
)

type sideThreadEntry struct {
	question string
	answer   string
	thinking string
	err      string
	complete bool
}

type sideThreadState struct {
	updates        <-chan runUpdate
	cancel         context.CancelFunc
	entries        []sideThreadEntry
	assistantEntry int
	status         string
	draft          string
	isVisible      bool
	isRunning      bool
	cancelPending  bool
	isClosed       bool
}

func btwSlashCommand() SlashCommand {
	return SlashCommand{
		Name:         "btw",
		Description:  "Ask an ephemeral side question without interrupting the main task",
		ArgumentHint: "[question]",
	}
}

func (m model) isBTWCommandInput() bool {
	request, slash := parseSlashCommand(m.input.Value())
	return slash && request.Name == "btw"
}

func (m model) openSideThread(question string) (model, tea.Cmd, bool) {
	if m.sideRequests == nil || m.sideControllerDone == nil || m.side.isClosed {
		return m.commandError(
			"/btw "+question,
			"The BTW side-thread controller is unavailable",
		)
	}

	question = strings.TrimSpace(question)
	m.side.isVisible = true
	m.input.Reset()
	m.input.Placeholder = sidePlaceholder
	m.commandSelection = 0
	m.commandDismissed = false
	m.historyIndex = -1
	m.historyDraft = ""

	if m.side.isRunning {
		if question != "" {
			m.side.draft = question
		}
		m.input.SetValue(m.side.draft)
		m.input.CursorEnd()
		m.input.Blur()
		m.side.status = "Current side answer is still running"
		return m.settleCommand(true, nil)
	}
	if question == "" {
		m.input.SetValue(m.side.draft)
		m.input.CursorEnd()
		m.input.Focus()
		if len(m.side.entries) == 0 {
			m.side.status = "Ask a quick question using context AICE already has"
		} else {
			m.side.status = "Side thread reopened"
		}
		return m.settleCommand(true, nil)
	}
	return m.startSideQuestion(question)
}

func (m model) submitSideComposer() (model, tea.Cmd, bool) {
	question := strings.TrimSpace(m.input.Value())
	if request, slash := parseSlashCommand(question); slash && request.Name == "btw" {
		question = strings.TrimSpace(request.Arguments)
	}
	if question == "" {
		m.side.status = "A side question is required"
		return m.settleCommand(false, nil)
	}
	return m.startSideQuestion(question)
}

func (m model) startSideQuestion(question string) (model, tea.Cmd, bool) {
	if m.sideRequests == nil || m.sideControllerDone == nil || m.side.isClosed {
		m.side.status = "The BTW side-thread controller is unavailable"
		return m.settleCommand(false, nil)
	}
	if m.side.isRunning {
		m.side.draft = question
		m.side.status = "Current side answer is still running"
		return m.settleCommand(false, nil)
	}

	m.side.entries = append(m.side.entries, sideThreadEntry{question: question})
	if len(m.side.entries) > maximumSideEntries {
		m.side.entries = append(
			[]sideThreadEntry{},
			m.side.entries[len(m.side.entries)-maximumSideEntries:]...,
		)
	}
	m.side.assistantEntry = len(m.side.entries) - 1
	m.side.isVisible = true
	m.side.isRunning = true
	m.side.cancelPending = false
	m.side.status = "Starting side answer..."
	m.side.draft = ""
	m.input.Reset()
	m.input.Placeholder = sidePlaceholder
	m.input.Blur()
	return m.settleCommand(
		true,
		startSideRun(
			m.sideRequests,
			m.sideControllerDone,
			question,
		),
	)
}

func (m model) closeSideThread() (model, tea.Cmd, bool) {
	m.side.draft = m.input.Value()
	m.side.isVisible = false
	m.input.Reset()
	m.input.Placeholder = defaultPlaceholder
	if m.composerInputEnabled() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.resizeLayout()
	m.refreshViewport(true)
	return m, nil, true
}

func (m model) handleSideKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if !m.side.isVisible {
		return m, nil, false
	}

	switch {
	case message.Code == tea.KeyEscape:
		return m.closeSideThread()
	case key.Matches(message, m.keys.interrupt):
		if !m.side.isRunning {
			m.side.status = "No side answer is running"
			return m, nil, true
		}
		if m.side.cancel != nil {
			m.side.cancel()
		} else {
			m.side.cancelPending = true
		}
		m.side.status = "Cancelling side answer..."
		m.refreshViewport(false)
		return m, nil, true
	case m.helpToggleRequested(message):
		m.help.ShowAll = !m.help.ShowAll
		m.resizeLayout()
		m.refreshViewport(false)
		return m, nil, true
	case key.Matches(message, m.keys.newline):
		if !m.side.isRunning {
			m.input.InsertString("\n")
			m.resizeLayout()
		}
		return m, nil, true
	case key.Matches(message, m.keys.send):
		if m.side.isRunning {
			return m, nil, true
		}
		return m.submitSideComposer()
	case key.Matches(message, m.keys.queue),
		key.Matches(message, m.keys.process):
		return m, nil, true
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

func (m model) applySideRunBatch(batch sideRunBatchMsg) (tea.Model, tea.Cmd) {
	if batch.source != m.side.updates {
		return m, nil
	}
	var commands []tea.Cmd
	follow := m.viewport.AtBottom()
	changed := false
	finished := false
	for _, update := range batch.updates {
		if update.cancel != nil {
			m.side.cancel = update.cancel
			if m.side.cancelPending {
				m.side.cancel()
				m.side.status = "Cancelling side answer..."
			} else {
				m.side.status = "Thinking..."
			}
			changed = true
		}
		if update.done {
			m.finishSideRun(update.err)
			finished = true
			changed = true
			continue
		}
		if m.applySideEvent(update.event) {
			changed = true
		}
	}

	if batch.closed {
		m.side.updates = nil
		if m.side.isRunning {
			m.finishSideRun(errors.New(
				"side run ended without a terminal update",
			))
			finished = true
			changed = true
		}
	} else if m.side.updates != nil {
		commands = append(commands, waitForSideRunUpdates(m.side.updates))
	}
	if changed && m.side.isVisible {
		m.resizeLayout()
		m.refreshViewport(follow || finished)
	}
	return m, tea.Batch(commands...)
}

func (m *model) applySideEvent(event DisplayEvent) bool {
	entry := m.activeSideEntry()
	switch event.Kind {
	case DisplayEventAssistantStart:
		if entry == nil {
			return false
		}
		entry.answer = ""
		entry.thinking = ""
		entry.err = ""
		entry.complete = false
		m.side.status = "Thinking..."
	case DisplayEventAssistantDelta:
		if entry == nil {
			return false
		}
		switch event.Delta.Kind {
		case DisplayDeltaText:
			entry.answer += event.Delta.Delta
			m.side.status = "Answering..."
		case DisplayDeltaThinking:
			entry.thinking += event.Delta.Delta
			m.side.status = "Thinking..."
		case DisplayDeltaToolCall:
			m.side.status = "Side questions cannot use tools"
		default:
			return false
		}
	case DisplayEventAssistantEnd:
		if entry == nil {
			return false
		}
		entry.answer = event.Assistant.Text
		entry.thinking = event.Assistant.Thinking
		entry.complete = true
		m.side.status = "Answer complete"
	case DisplayEventToolStart, DisplayEventToolEnd:
		m.side.status = "Side questions cannot use tools"
	case DisplayEventRetryStart:
		m.side.status = "Retrying side answer..."
	case DisplayEventRetryEnd:
		if event.Retry.Succeeded {
			m.side.status = "Retry succeeded"
		} else {
			m.side.status = "Retry stopped"
		}
	case DisplayEventAgentEnd:
		if event.Err == nil {
			m.side.status = "Answer complete"
		}
	default:
		return false
	}
	return true
}

func (m *model) activeSideEntry() *sideThreadEntry {
	if m.side.assistantEntry < 0 ||
		m.side.assistantEntry >= len(m.side.entries) {
		return nil
	}
	return &m.side.entries[m.side.assistantEntry]
}

func (m *model) finishSideRun(err error) {
	if err != nil {
		message := err.Error()
		if errors.Is(err, context.Canceled) {
			message = "Side answer cancelled"
		}
		if entry := m.activeSideEntry(); entry != nil {
			entry.err = message
			entry.complete = true
		}
		m.side.status = message
	} else {
		if entry := m.activeSideEntry(); entry != nil {
			entry.complete = true
		}
		m.side.status = "Ready for another side question"
	}
	m.side.isRunning = false
	m.side.cancel = nil
	m.side.cancelPending = false
	m.side.assistantEntry = -1
	if m.side.isVisible {
		m.input.SetValue(m.side.draft)
		m.input.CursorEnd()
		m.input.Focus()
	}
}

func (m model) sideThreadView() string {
	parts := []transcriptViewPart{{content: m.sideThreadIntro()}}
	for index, entry := range m.side.entries {
		parts = append(parts, transcriptViewPart{
			content: m.sideQuestionView(entry.question),
		})
		if answer := m.sideAnswerView(
			entry,
			m.side.isRunning && index == m.side.assistantEntry,
		); answer != "" {
			parts = append(parts, transcriptViewPart{content: answer})
		}
	}
	return joinTranscriptViewParts(parts)
}

func (m model) sideThreadIntro() string {
	title := headerStyle.Render("↗ BTW SIDE THREAD")
	detail := mutedStyle.Render(
		"Ephemeral · no tools · excluded from the main Session",
	)
	if len(m.side.entries) == 0 {
		detail += "\n\n" + bodyStyle.Render(
			"Ask about context AICE already gathered while the main task keeps running.",
		)
	}
	if strings.TrimSpace(m.side.status) != "" {
		detail += "\n" + infoStyle.Render(m.side.status)
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(title + "\n" + detail)
}

func (m model) sideQuestionView(question string) string {
	bodyWidth := max(m.contentWidth()-userStyle.GetHorizontalFrameSize(), 1)
	body := userStyle.Width(bodyWidth).Render(question)
	return lipgloss.NewStyle().Padding(0, 1).Render(
		labelStyle.Render("YOU / BTW") + "\n" + body,
	)
}

func (m model) sideAnswerView(entry sideThreadEntry, active bool) string {
	bodyWidth := max(
		m.contentWidth()-assistantBodyStyle.GetHorizontalFrameSize(),
		1,
	)
	parts := make([]string, 0, 3)
	if strings.TrimSpace(entry.thinking) != "" {
		thinkingWidth := max(
			bodyWidth-thinkingStyle.GetHorizontalFrameSize(),
			1,
		)
		parts = append(parts, assistantBodyStyle.Render(
			thinkingStyle.Width(thinkingWidth).Render(entry.thinking),
		))
	}
	if strings.TrimSpace(entry.answer) != "" {
		parts = append(parts, assistantBodyStyle.Render(
			renderMarkdown(entry.answer, m.contentWidth()),
		))
	}
	if entry.err != "" {
		parts = append(parts, errorStyle.Render("✕ "+entry.err))
	}
	if entry.complete &&
		strings.TrimSpace(entry.answer) == "" &&
		strings.TrimSpace(entry.thinking) == "" &&
		entry.err == "" {
		parts = append(parts, mutedStyle.Render("No text response"))
	}
	if active &&
		!entry.complete &&
		strings.TrimSpace(entry.answer) == "" &&
		entry.err == "" {
		parts = append(parts, assistantBodyStyle.Render(
			m.spinner.View()+" "+mutedStyle.Render(m.side.status),
		))
	}
	if len(parts) == 0 {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(
		headerStyle.Render("✦ AICE / BTW") + "\n\n" +
			strings.Join(parts, "\n"),
	)
}

func (m model) sideStatusLine(width int) string {
	left := mutedStyle.Render("enter ask  shift+enter newline  esc close")
	if m.side.isRunning {
		left = mutedStyle.Render("esc close  ctrl+C cancel side answer")
	}
	if m.help.ShowAll {
		left = mutedStyle.Render(
			"enter ask  shift+enter newline  pgup/pgdn scroll  esc close  ctrl+C cancel",
		)
	}
	if line, ok := alignStatusLine(left, m.modelStatus(), width); ok {
		return line
	}
	if line, ok := alignStatusLine("", m.modelStatus(), width); ok {
		return line
	}
	return ""
}

type sideRunStartedMsg struct {
	updates <-chan runUpdate
}

type sideRunUnavailableMsg struct{}

type sideRunBatchMsg struct {
	source  <-chan runUpdate
	updates []runUpdate
	closed  bool
}

func startSideRun(
	requests chan<- runRequest,
	controllerDone <-chan struct{},
	prompt string,
) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan runUpdate, runUpdateBuffer)
		select {
		case <-controllerDone:
			return sideRunUnavailableMsg{}
		case requests <- runRequest{prompt: prompt, updates: updates}:
			return sideRunStartedMsg{updates: updates}
		}
	}
}

func waitForSideRunUpdates(updates <-chan runUpdate) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-updates
		if !ok {
			return sideRunBatchMsg{source: updates, closed: true}
		}
		batch := sideRunBatchMsg{
			source:  updates,
			updates: []runUpdate{first},
		}
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
