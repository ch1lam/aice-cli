package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m *model) beginProcess() int {
	m.nextProcessID++
	m.activeProcessID = m.nextProcessID
	m.processGroups = append(m.processGroups, processGroup{
		id:        m.activeProcessID,
		startedAt: time.Now(),
	})
	return m.activeProcessID
}

func (m *model) ensureActiveProcess() int {
	if m.activeProcessID == 0 {
		return m.beginProcess()
	}
	return m.activeProcessID
}

func (m *model) processGroup(processID int) *processGroup {
	for index := range m.processGroups {
		if m.processGroups[index].id == processID {
			return &m.processGroups[index]
		}
	}
	return nil
}

func (m *model) markConclusion() bool {
	if m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		return false
	}
	entry := &m.entries[m.assistantEntry]
	if entry.kind != entryAssistant {
		return false
	}

	changed := !entry.conclusion
	entry.conclusion = true
	if group := m.processGroup(entry.processID); group != nil && !group.manual {
		changed = changed || !group.collapsed
		group.collapsed = true
	}
	return changed
}

func (m *model) revokeConclusion() bool {
	if m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		return false
	}
	entry := &m.entries[m.assistantEntry]
	if entry.kind != entryAssistant {
		return false
	}

	changed := entry.conclusion
	entry.conclusion = false
	if group := m.processGroup(entry.processID); group != nil && !group.manual {
		changed = changed || group.collapsed
		group.collapsed = false
	}
	return changed
}

func (m *model) toggleProcessGroups() bool {
	expand := false
	found := false
	for _, group := range m.processGroups {
		if !m.hasProcessContent(group.id) {
			continue
		}
		found = true
		if group.collapsed {
			expand = true
			break
		}
	}
	if !found {
		return false
	}

	for index := range m.processGroups {
		group := &m.processGroups[index]
		if m.hasProcessContent(group.id) {
			group.collapsed = !expand
			group.manual = true
		}
	}
	m.expandAllDetails(expand)
	return true
}

func (m model) hasProcessContent(processID int) bool {
	for index, entry := range m.entries {
		if entry.processID != processID {
			continue
		}
		switch entry.kind {
		case entryTool:
			return true
		case entryAssistant:
			if strings.TrimSpace(entry.thinking) != "" {
				return true
			}
			if !entry.conclusion && strings.TrimSpace(entry.text) != "" {
				return true
			}
			if !entry.conclusion &&
				index == m.assistantEntry &&
				!entry.complete {
				return true
			}
		}
	}
	return false
}

func (m model) applyRunBatch(batch runBatchMsg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	follow := m.viewport.AtBottom()
	contentChanged := false
	finished := false
	for _, update := range batch.updates {
		if update.active != nil {
			m.submittedInput = nil
			m.submittedDraft = composerDraft{}
			m.activeRun = update.active
			m.acceptsDelivery = true
		}
		if update.cancel != nil {
			m.cancelRun = update.cancel
			if m.cancelRequested {
				m.cancelRun()
				m.status = "Cancelling current response..."
			} else {
				m.status = "Thinking..."
			}
		}
		if update.auth != nil && m.authInput != nil && !m.cancelRequested {
			prompt := *update.auth
			m.authPrompt = &prompt
			m.authSelection = 0
			m.input.Placeholder = prompt.Title + "; Escape or Ctrl+C cancels"
			if prompt.AllowInput {
				m.input.Placeholder = "Authorization code / redirect URL (input hidden)"
				commands = append(commands, m.input.Focus())
			}
			if prompt.InputLabel != "" {
				m.input.Placeholder = prompt.InputLabel
			}
			if prompt.Menu != nil {
				m.input.Blur()
			}
			m.status = prompt.Title + "; Escape or Ctrl+C cancels"
			m.resizeLayout()
			contentChanged = true
		}
		if strings.TrimSpace(update.output) != "" {
			m.entries = append(m.entries, transcriptEntry{
				kind: entryCommand,
				text: strings.TrimSpace(update.output),
			})
			contentChanged = true
		}
		if update.state != nil {
			m.contextUsage = update.state.Context
			m.currentModel = update.state.Model
			m.thinking = update.state.Thinking
			m.apiKeyConfigured = update.state.APIKeyConfigured
			if update.state.Usage != m.sessionUsage {
				commands = append(
					commands,
					m.usageAnimation.Start(m.sessionUsage, update.state.Usage),
				)
				m.sessionUsage = update.state.Usage
			}
			if update.state.SessionChanged {
				m.resetBranchTranscript()
				contentChanged = true
			}
		}
		if update.commands != nil {
			commands := *update.commands
			if m.sideRequests != nil {
				commands = append(commands, btwSlashCommand())
			}
			m.commands = slashCommandCatalog(commands)
		}
		if update.done {
			if update.err != nil {
				m.restoreSubmittedInput()
			}
			commands = append(commands, m.finishRun(update.err))
			finished = true
			continue
		}
		changed, command := m.applyAgentEvent(update.event)
		contentChanged = changed || contentChanged
		if command != nil {
			commands = append(commands, command)
		}
	}

	if batch.closed {
		m.updates = nil
		if m.running {
			m.restoreSubmittedInput()
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

func (m *model) applyAgentEvent(event DisplayEvent) (bool, tea.Cmd) {
	if event.Context != nil {
		m.contextUsage = *event.Context
	}
	switch event.Kind {
	case DisplayEventAssistantStart:
		m.entries = append(m.entries, transcriptEntry{
			kind:         entryAssistant,
			presentation: &assistantPresentation{},
			processID:    m.ensureActiveProcess(),
		})
		m.assistantEntry = len(m.entries) - 1
		m.status = "Thinking..."
		return true, nil
	case DisplayEventAssistantDelta:
		return m.applyAssistantDelta(event), nil
	case DisplayEventAssistantEnd:
		return true, m.completeAssistant(event.Assistant)
	case DisplayEventToolStart:
		m.revokeConclusion()
		m.startDisplayedTool(event.Tool)
		m.status = "Running " + event.Tool.Name + "..."
		return true, nil
	case DisplayEventToolEnd:
		m.completeTool(event.Tool)
		m.status = "Thinking..."
		return true, nil
	case DisplayEventSteer:
		return m.applySteer(event.Input), nil
	case DisplayEventFollowUp:
		m.startQueuedDelivery(pendingDelivery{
			id:   event.Input.ID,
			text: event.Input.Text,
			mode: deliveryQueue,
		})
		return true, nil
	case DisplayEventRetryStart:
		m.status = fmt.Sprintf(
			"Retrying in %s (%d/%d)...",
			event.Retry.Delay,
			event.Retry.Attempt,
			event.Retry.MaxRetries,
		)
		return true, nil
	case DisplayEventRetryEnd:
		if event.Retry.Succeeded {
			m.status = "Retry succeeded"
		} else {
			m.status = "Retry stopped"
		}
		return true, nil
	case DisplayEventAgentEnd:
		if event.Err == nil {
			m.status = "Response complete"
		}
	}
	return false, nil
}

func (m *model) applyAssistantDelta(event DisplayEvent) bool {
	if m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		return false
	}
	entry := &m.entries[m.assistantEntry]
	if entry.presentation == nil {
		entry.presentation = &assistantPresentation{}
	}
	switch event.Delta.Kind {
	case DisplayDeltaText:
		entry.text = entry.presentation.appendText(entry.text, event.Delta.Delta)
		if strings.TrimSpace(entry.text) != "" {
			m.markConclusion()
		}
		m.status = "Responding..."
	case DisplayDeltaThinking:
		entry.thinking = entry.presentation.appendThinking(entry.thinking, event.Delta.Delta)
		m.status = "Thinking..."
	case DisplayDeltaToolCall:
		changed := m.revokeConclusion()
		return m.applyWriteDelta(event.Delta) || changed
	default:
		return false
	}
	return true
}

func (m *model) completeAssistant(display AssistantDisplay) tea.Cmd {
	if m.assistantEntry < 0 || m.assistantEntry >= len(m.entries) {
		m.entries = append(m.entries, transcriptEntry{
			kind:         entryAssistant,
			presentation: &assistantPresentation{},
			processID:    m.ensureActiveProcess(),
		})
		m.assistantEntry = len(m.entries) - 1
	}
	entry := &m.entries[m.assistantEntry]
	entry.text = display.Text
	entry.thinking = display.Thinking
	entry.complete = true
	entry.presentation = &assistantPresentation{}
	if display.Concludes {
		m.markConclusion()
	} else {
		m.revokeConclusion()
	}
	return nil
}

func (m *model) resetBranchTranscript() {
	kept := make([]transcriptEntry, 0, len(m.entries))
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := m.entries[index]
		if entry.kind != entryUser &&
			entry.kind != entryCommand &&
			entry.kind != entryNotice &&
			entry.kind != entryError {
			continue
		}
		kept = append(kept, entry)
		if entry.kind == entryUser {
			break
		}
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	m.entries = kept
	m.processGroups = nil
	m.folds = nil
	m.activeProcessID = 0
	m.assistantEntry = -1
	m.refreshViewport(true)
}

func (m *model) completeTool(tool ToolDisplay) {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := &m.entries[index]
		if entry.kind == entryTool && entry.toolID == tool.ID && !entry.toolDone {
			entry.toolDone = true
			entry.toolError = tool.Failed
			entry.toolTruncation = tool.Truncation
			entry.toolOutput = tool.Output
			if entry.toolName == "edit" && !tool.Failed {
				entry.toolDiff = tool.Diff
			}
			return
		}
	}
}

func (m *model) finishRun(err error) tea.Cmd {
	if m.cancelDelivery != nil {
		m.cancelDelivery()
	}
	wasAuth := m.authInput != nil
	wasBrowser := m.authCommand == "browser"
	m.authCommand = ""
	if wasAuth {
		m.authInput = nil
		m.authPrompt = nil
		m.resetCommandInput()
	}
	if err != nil {
		m.revokeConclusion()
	}
	m.updateActiveProcessDuration(time.Now())
	m.running = false
	m.acceptsDelivery = false
	m.activeRun = nil
	m.pendingDeliveries = nil
	m.cancelRun = nil
	m.cancelRequested = false
	m.assistantEntry = -1
	m.activeProcessID = 0
	var focus tea.Cmd
	if m.composerInputEnabled() {
		focus = m.input.Focus()
	} else {
		m.input.Blur()
	}
	if err != nil {
		message := err.Error()
		if errors.Is(err, context.Canceled) {
			message = "Response cancelled"
			if wasAuth {
				message = "Login cancelled"
				if wasBrowser {
					message = "Browser command cancelled"
				}
			}
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
	input RunInput,
) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan runUpdate, runUpdateBuffer)
		select {
		case <-controllerDone:
			return runUnavailableMsg{}
		case requests <- runRequest{
			prompt:  input.Prompt,
			files:   input.Files,
			images:  input.Images,
			updates: updates,
		}:
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

// streamBatchInterval groups token deltas that arrive just after the channel
// was drained. Lifecycle updates flush immediately; keyboard input never waits
// on this command.
const streamBatchInterval = 16 * time.Millisecond

func waitForRunUpdates(updates <-chan runUpdate) tea.Cmd {
	return func() tea.Msg { return collectRunUpdates(updates) }
}

func collectRunUpdates(updates <-chan runUpdate) runBatchMsg {
	first, ok := <-updates
	if !ok {
		return runBatchMsg{closed: true}
	}
	batch := runBatchMsg{updates: []runUpdate{first}}
	if first.event.Kind != DisplayEventAssistantDelta {
		return batch
	}
	timer := time.NewTimer(streamBatchInterval)
	defer timer.Stop()
	for len(batch.updates) < maximumEventBatch {
		select {
		case update, open := <-updates:
			if !open {
				batch.closed = true
				return batch
			}
			batch.updates = append(batch.updates, update)
			if update.event.Kind != DisplayEventAssistantDelta {
				return batch
			}
		case <-timer.C:
			return batch
		}
	}
	return batch
}
