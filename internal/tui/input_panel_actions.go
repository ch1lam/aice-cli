package tui

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// panelFocused reports whether the keyboard is on the panel below the
// composer.
func (m model) panelFocused() bool {
	return m.panelFocus && (len(m.agents) > 0 || len(m.shellTasks()) > 0)
}

// selectedPanelIndex is the position of the selected row in panelItems: the
// named one, or else the top. Selection follows an ID, so rows that come and
// go do not move it.
func (m model) selectedPanelIndex(items []panelItem) int {
	return max(slices.IndexFunc(items, func(item panelItem) bool { return item.id() == m.panelSelected }), 0)
}

// panelReachable reports whether Down in the main composer moves to the
// panel: it has rows, the composer is not recalling history, and the caret
// is on the draft's last row. Runs do not block it; the panel matters most
// while tasks and children run.
func (m model) panelReachable() bool {
	return (len(m.agents) > 0 || len(m.shellTasks()) > 0) && m.historyIndex < 0 &&
		m.composerCaretOnLastRow() && !m.slashCommandMenuVisible() && !m.fileCompletionVisible()
}

// focusPanel moves the keyboard to the panel below the composer, on its
// top row, the one Down reached.
func (m *model) focusPanel() {
	m.panelFocus = true
	m.panelSelected = m.panelItems()[0].id()
	m.resizeLayout()
	m.refreshViewport(false)
}

// clickPanelRow is Enter on a clicked row: the panel takes the keyboard on
// that row, which opens; closing it returns to the panel.
func (m model) clickPanelRow(item panelItem) (model, tea.Cmd) {
	m.panelFocus, m.panelSelected, m.panelStopArmed = true, item.id(), ""
	m.input.Blur()
	m.resizeLayout()
	return m.openPanelItem(item)
}

func (m model) openPanelItem(item panelItem) (model, tea.Cmd) {
	if item.task != nil {
		return m.openTask(*item.task)
	}
	return m.openAgent(item.agent), nil
}

// blurPanel returns the keyboard to the composer.
func (m *model) blurPanel() tea.Cmd {
	m.panelFocus, m.panelStopArmed = false, ""
	m.resizeLayout()
	m.refreshViewport(false)
	return m.input.Focus()
}

// stoppable reports whether x can stop the row: a shell task, or a running
// background subagent; a synchronous one stops with its response.
func (m model) stoppable(item panelItem) bool {
	if item.task != nil {
		return m.stopTask != nil
	}
	return m.stopAgent != nil && item.agent.running && item.agent.start.Background
}

func (m model) panelInputBindings() []inputBinding {
	items := m.panelItems()
	stop := actionBinding(inputActionPanelStop, "x", "stop", "x")
	stop.binding.SetEnabled(false)
	if len(items) > 0 {
		item := items[m.selectedPanelIndex(items)]
		stop.binding.SetEnabled(m.stoppable(item))
		if m.panelStopArmed == item.id() {
			stop.binding.SetHelp("x", "again to stop")
		}
	}
	return []inputBinding{
		actionBinding(inputActionPanelOpen, "Enter", "open", "enter"),
		actionBinding(inputActionPanelBack, "Esc", "back", "esc", "ctrl+c"),
		{action: inputActionPanelMove, binding: key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "select")), argument: -1, short: true},
		{action: inputActionPanelMove, binding: key.NewBinding(key.WithKeys("down"), key.WithHelp("↑/↓", "select")), argument: 1, short: true},
		stop,
	}
}

// handlePanelAction applies a panel action. Stopping takes x twice on the
// same row; any other action disarms the first press.
func (m model) handlePanelAction(match inputActionMatch) (model, tea.Cmd, bool) {
	armed := m.panelStopArmed
	m.panelStopArmed = ""
	items := m.panelItems()
	if len(items) == 0 {
		return m, m.blurPanel(), true
	}
	item := items[m.selectedPanelIndex(items)]
	switch match.action {
	case inputActionPanelMove:
		next := m.selectedPanelIndex(items) + match.argument
		if next < 0 {
			// Up from the top row returns to the composer above.
			return m, m.blurPanel(), true
		}
		m.panelSelected = items[clampIndex(next, len(items))].id()
	case inputActionPanelOpen:
		next, command := m.openPanelItem(item)
		return next, command, true
	case inputActionPanelStop:
		if armed != item.id() {
			m.panelStopArmed = item.id()
			return m, nil, true
		}
		if item.task != nil {
			return m, m.stopTask(item.task.ID), true
		}
		item.agent.activity = "Stopping"
		return m, m.stopAgent(item.agent.id), true
	case inputActionPanelBack:
		return m, m.blurPanel(), true
	}
	return m, nil, true
}

// openAgent shows a child's transcript in the reader, which follows it live:
// the main model keeps receiving the child's updates underneath, and the
// reader copies the child's transcript after each.
func (m model) openAgent(view *agentView) model {
	reader := newModel(nil, nil)
	reader.width, reader.height = m.width, m.height
	reader.currentModel, reader.workingDirectory = view.start.Model, m.workingDirectory
	reader.reading = &sessionReading{previous: &m, agent: view.id}
	reader.input.Blur()
	reader.followAgent(m)
	reader.resizeLayout()
	reader.viewport.GotoBottom()
	return reader
}

// followAgent copies the followed child's transcript from the live model.
// Folds made in the reader are kept; the reader stays at the bottom while
// it is there, as the live transcript does.
func (m *model) followAgent(live model) {
	view := live.agentView(m.reading.agent)
	if view == nil {
		return
	}
	collapsed := make(map[int]bool, len(m.processGroups))
	for _, group := range m.processGroups {
		collapsed[group.id] = group.collapsed
	}
	t := view.transcript
	m.entries = slices.Clone(t.entries)
	for i := range m.entries {
		if m.entries[i].kind == entryAssistant {
			m.entries[i].presentation = &assistantPresentation{}
		}
	}
	m.processGroups = slices.Clone(t.processGroups)
	for i, group := range m.processGroups {
		if folded, ok := collapsed[group.id]; ok {
			m.processGroups[i].collapsed = folded
		}
	}
	m.assistantEntry, m.activeProcessID, m.status = t.assistantEntry, t.activeProcessID, t.status
	m.run = t.run
	m.spinner = live.spinner
	m.refreshViewport(false)
}

// agentStopMsg reports a stop request; a failed one is shown.
type agentStopMsg struct {
	id  string
	err error
}

func agentStopCommand(agents interaction.Agents) func(string) tea.Cmd {
	return func(id string) tea.Cmd {
		return func() tea.Msg { return agentStopMsg{id: id, err: agents.StopAgent(id)} }
	}
}

func (m model) applyAgentStop(message agentStopMsg) (tea.Model, tea.Cmd) {
	if message.err == nil {
		return m, nil
	}
	if view := m.agentView(message.id); view != nil && view.running {
		view.activity = "Running"
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryError, text: "Could not stop subagent " + message.id + ": " + message.err.Error()})
	m.refreshViewport(true)
	return m, nil
}

// agentReadingLabel heads a reader that follows a child: which child, and
// what it does or how it ended.
func (m model) agentReadingLabel() (string, bool) {
	if m.reading == nil || m.reading.agent == "" || m.reading.previous == nil {
		return "", false
	}
	view := m.reading.previous.agentView(m.reading.agent)
	if view == nil {
		return "SUBAGENT · " + m.reading.agent, true
	}
	label := "SUBAGENT · " + sanitizeSingleLineText(view.start.Description) + " · " + sanitizeSingleLineText(view.start.Model.ID)
	if status := view.status(); status.running {
		label = m.spinner.View() + " " + label + " · " + status.text
	} else {
		label += " · " + status.text
	}
	return label, true
}
