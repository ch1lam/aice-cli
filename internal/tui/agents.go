package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// activity is what the running child does now, as the panel shows it;
	// tools counts the tool calls its runs finished, as progress.
	activity string
	tools    int
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
	running := m.runningAgents()
	started := false
	for _, update := range batch.updates {
		m.applyAgentUpdate(update, now)
		started = started || update.Start != nil
	}
	if len(m.runningAgents()) != len(running) {
		// The panel above the composer grew or shrank.
		m.resizeLayout()
	}
	m.refreshViewport(false)
	var commands []tea.Cmd
	if started {
		// Animates the panel while no run does; extra tick chains collapse.
		commands = append(commands, m.spinner.Tick)
	}
	if batch.closed {
		m.agentUpdates = nil
	} else {
		commands = append(commands, waitForAgentUpdates(m.agentUpdates))
	}
	return m, tea.Batch(commands...)
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
		view.activity = "Starting"
	case view == nil || !view.running:
		return
	case update.Event != nil:
		view.transcript.applyAgentEvent(*update.Event)
		if update.Event.Kind == interaction.EventToolEnd {
			view.tools++
		}
		previous := view.activity
		view.activity = agentActivity(*update.Event, view.activity)
		if view.activity == previous {
			return
		}
	case update.End != nil:
		view.running, view.end, view.activity = false, update.End, ""
		view.elapsed += update.End.Duration
		view.transcript.settleAgentRun(update.End.Err, now)
	}
	m.showAgentOnCall(view)
	if update.End != nil {
		m.pruneAgents()
	}
}

// agentActivity names what a child does after event, keeping current when
// the event changes nothing.
func agentActivity(event interaction.Event, current string) string {
	switch event.Kind {
	case interaction.EventAssistantStart, interaction.EventToolEnd:
		return "Thinking"
	case interaction.EventAssistantDelta:
		switch event.Delta.Kind {
		case interaction.DeltaText:
			return "Responding"
		case interaction.DeltaThinking:
			return "Thinking"
		}
	case interaction.EventToolStart:
		tool := event.Tool
		detail := tool.Detail
		if tool.Description != "" {
			detail = tool.Description
		}
		activity := "Running " + sanitizeSingleLineText(tool.Name)
		if detail = strings.Join(strings.Fields(sanitizeSingleLineText(detail)), " "); detail != "" {
			activity += " " + detail
		}
		return activity
	case interaction.EventRetryStart:
		return "Retrying"
	}
	return current
}

func (m model) runningAgents() []*agentView {
	var running []*agentView
	for _, view := range m.agents {
		if view.running {
			running = append(running, view)
		}
	}
	return running
}

// showAgentOnCall writes the child's state onto the agent call that started
// it in the main transcript. The call's card may arrive after the child's
// first updates, since they travel apart; a later update links it then.
func (m *model) showAgentOnCall(view *agentView) {
	call := view.start.ToolCallID
	if call == "" {
		return
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		entry := &m.entries[i]
		if entry.kind == entryTool && entry.toolName == "agent" && entry.toolID == call {
			entry.toolAgent = view.status()
			return
		}
	}
}

// agentStatus is a child's state shown on its call; comparable, so the
// card's cached view changes with it.
type agentStatus struct {
	running, failed, stopped bool
	text                     string
}

func (v *agentView) status() agentStatus {
	if v.running {
		return agentStatus{running: true, text: v.activity}
	}
	switch end := v.end; {
	case end == nil:
		return agentStatus{}
	case errors.Is(end.Err, context.Canceled):
		return agentStatus{stopped: true, text: "stopped after " + formatRunDuration(v.elapsed)}
	case end.Err != nil:
		return agentStatus{failed: true, text: "failed: " + sanitizeSingleLineText(end.Err.Error())}
	}
	return agentStatus{text: fmt.Sprintf("completed in %s · %s · %s", formatRunDuration(v.elapsed),
		count(v.end.Turns, "turn"), count(v.end.ToolCalls, "tool call"))}
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
