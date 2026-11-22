package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// maximumFinishedAgents bounds the finished subagents kept for display; the
// oldest go first. Running ones are always kept.
const maximumFinishedAgents = 16

// agentView is one subagent as the TUI shows it. Its transcript is a
// presentation-only model fed by the child's events, as the main transcript
// is fed by the main run's, so both render the same way.
type agentView struct {
	id    string
	start interaction.AgentStart
	// transcript holds every run of the child, a continued one included.
	transcript *model
	running    bool
	// startedAt is when the current run started; elapsed sums finished runs.
	startedAt time.Time
	elapsed   time.Duration
	// end settles the last run; nil while one runs.
	end *interaction.AgentEnd
}

// agentUpdatesMsg is one batch of subagent updates; closed reports that the
// application stopped delivering them.
type agentUpdatesMsg struct {
	updates []interaction.AgentUpdate
	closed  bool
}

// waitForAgentUpdates receives the next batch as collectRunUpdates does for
// a run: lifecycle updates at once, streamed text grouped for a moment.
func waitForAgentUpdates(updates <-chan interaction.AgentUpdate) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-updates
		if !ok {
			return agentUpdatesMsg{closed: true}
		}
		batch := agentUpdatesMsg{updates: []interaction.AgentUpdate{first}}
		if first.Event == nil || first.Event.Kind != interaction.EventAssistantDelta {
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
			case <-timer.C:
				return batch
			}
		}
		return batch
	}
}

func (m model) applyAgentUpdates(batch agentUpdatesMsg) (tea.Model, tea.Cmd) {
	now := time.Now()
	for _, update := range batch.updates {
		m.applyAgentUpdate(update, now)
	}
	if batch.closed {
		m.agentUpdates = nil
		return m, nil
	}
	return m, waitForAgentUpdates(m.agentUpdates)
}

// applyAgentUpdate folds one update into its view. Only a start creates a
// view, so updates of children dropped with their Session are ignored.
func (m *model) applyAgentUpdate(update interaction.AgentUpdate, now time.Time) {
	view := m.agentView(update.ID)
	switch {
	case update.Start != nil:
		if view == nil {
			transcript := newModel(nil, nil)
			view = &agentView{id: update.ID, transcript: &transcript}
			m.agents = append(m.agents, view)
		}
		view.start, view.running, view.startedAt, view.end = *update.Start, true, now, nil
		transcript := view.transcript
		transcript.entries = append(transcript.entries, transcriptEntry{kind: entryUser, text: update.Start.Prompt})
		transcript.activeProcessID = 0
		transcript.beginProcess()
		transcript.status = "Starting..."
	case view == nil || !view.running:
	case update.Event != nil:
		view.transcript.applyAgentEvent(*update.Event)
	case update.End != nil:
		view.running, view.end = false, update.End
		view.elapsed += update.End.Duration
		view.transcript.settleAgentRun(update.End.Err, now)
		m.pruneAgents()
	}
}

func (m *model) agentView(id string) *agentView {
	for _, view := range m.agents {
		if view.id == id {
			return view
		}
	}
	return nil
}

// pruneAgents drops the oldest finished views beyond the bound.
func (m *model) pruneAgents() {
	finished := 0
	for _, view := range m.agents {
		if !view.running {
			finished++
		}
	}
	if finished <= maximumFinishedAgents {
		return
	}
	// A new slice: earlier copies of the model keep theirs.
	kept := make([]*agentView, 0, len(m.agents))
	for _, view := range m.agents {
		if !view.running && finished > maximumFinishedAgents {
			finished--
			continue
		}
		kept = append(kept, view)
	}
	m.agents = kept
}

// settleAgentRun closes a child run in its transcript as finishRun closes
// the main one: the open process stops, a tool whose end never arrived
// failed, and an error is shown.
func (m *model) settleAgentRun(err error, now time.Time) {
	if err != nil {
		m.revokeConclusion()
	}
	m.updateActiveProcessDuration(now)
	for i := range m.entries {
		if e := &m.entries[i]; e.kind == entryTool && e.toolState == toolRunning {
			e.toolState = toolFailed
		}
	}
	m.assistantEntry, m.activeProcessID = -1, 0
	m.status = ""
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		m.entries = append(m.entries, transcriptEntry{kind: entryNotice, text: "Subagent stopped"})
	default:
		m.entries = append(m.entries, transcriptEntry{kind: entryError, text: err.Error()})
	}
}
