package tui

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// agentsFocused reports whether the keyboard is on the subagent panel.
func (m model) agentsFocused() bool { return m.agentFocus && len(m.agents) > 0 }

// selectedAgentIndex is the position of the selected child in agents: the
// named one, or else the latest. Selection follows an ID, so pruning older
// children does not move it.
func (m model) selectedAgentIndex() int {
	if i := slices.IndexFunc(m.agents, func(view *agentView) bool { return view.id == m.agentSelected }); i >= 0 {
		return i
	}
	return len(m.agents) - 1
}

func (m model) selectedAgent() *agentView {
	if len(m.agents) == 0 {
		return nil
	}
	return m.agents[m.selectedAgentIndex()]
}

// agentPanelReachable reports whether Down in the main composer moves to
// the panel: children exist, the composer is not recalling history, and the
// caret is on the draft's last row. Runs do not block it; the panel matters
// most while children run.
func (m model) agentPanelReachable() bool {
	return len(m.agents) > 0 && m.historyIndex < 0 && m.composerCaretOnLastRow() &&
		!m.slashCommandMenuVisible() && !m.fileCompletionVisible()
}

// focusAgents moves the keyboard to the panel below the composer, on its
// top row, the one Down reached.
func (m *model) focusAgents() {
	m.agentFocus = true
	m.agentSelected = m.agents[0].id
	m.resizeLayout()
	m.refreshViewport(false)
}

// blurAgents returns the keyboard to the composer.
func (m *model) blurAgents() tea.Cmd {
	m.agentFocus = false
	m.resizeLayout()
	m.refreshViewport(false)
	return m.input.Focus()
}

func (m model) agentInputBindings() []inputBinding {
	view := m.selectedAgent()
	stop := actionBinding(inputActionAgentStop, "x", "stop", "x")
	stop.binding.SetEnabled(m.stopAgent != nil && view != nil && view.running && view.start.Background)
	return []inputBinding{
		actionBinding(inputActionAgentOpen, "Enter", "open", "enter"),
		actionBinding(inputActionAgentBack, "Esc", "back", "esc", "ctrl+c"),
		{action: inputActionAgentMove, binding: key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/↓", "select")), argument: -1, short: true},
		{action: inputActionAgentMove, binding: key.NewBinding(key.WithKeys("down"), key.WithHelp("↑/↓", "select")), argument: 1, short: true},
		stop,
	}
}

func (m model) handleAgentAction(match inputActionMatch) (model, tea.Cmd, bool) {
	switch match.action {
	case inputActionAgentMove:
		next := m.selectedAgentIndex() + match.argument
		if next < 0 {
			// Up from the top row returns to the composer above.
			return m, m.blurAgents(), true
		}
		m.agentSelected = m.agents[clampIndex(next, len(m.agents))].id
	case inputActionAgentOpen:
		return m.openAgent(m.selectedAgent()), nil, true
	case inputActionAgentStop:
		view := m.selectedAgent()
		view.activity = "Stopping"
		return m, m.stopAgent(view.id), true
	case inputActionAgentBack:
		return m, m.blurAgents(), true
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
