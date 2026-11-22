package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func agentStart(id, prompt string) interaction.AgentUpdate {
	return interaction.AgentUpdate{ID: id, Start: &interaction.AgentStart{
		ToolCallID: "call-" + id, Description: "find loader", Type: "explore",
		Model: interaction.DisplayModel{ID: "child-model"}, Prompt: prompt,
	}}
}

func agentEvent(id string, event interaction.Event) interaction.AgentUpdate {
	return interaction.AgentUpdate{ID: id, Event: &event}
}

func agentEnd(id string, err error, duration time.Duration) interaction.AgentUpdate {
	return interaction.AgentUpdate{ID: id, End: &interaction.AgentEnd{Err: err, Duration: duration, Turns: 1, ToolCalls: 1}}
}

func applyAgents(t *testing.T, m model, updates ...interaction.AgentUpdate) model {
	t.Helper()
	return updateModel(t, m, agentUpdatesMsg{updates: updates})
}

// A child's events build its own transcript as the main run's build the main
// one, its status names what it is doing, and its end settles it.
func TestAgentUpdatesBuildTheChildTranscript(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 80, 24),
		agentStart("bg-1", "find the config loader"),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventAssistantStart}),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventAssistantEnd, Assistant: interaction.AssistantDisplay{Text: "looking"}}),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolStart, Tool: interaction.ToolDisplay{ID: "t1", Name: "ls", Detail: "."}}),
	)
	if len(m.entries) != 0 {
		t.Fatalf("a child changed the main transcript: %#v", m.entries)
	}
	view := m.agentView("bg-1")
	if view == nil || !view.running || view.start.Description != "find loader" {
		t.Fatalf("view = %#v", view)
	}
	if got := view.transcript.status; got != "Running ls..." {
		t.Fatalf("activity = %q, want the running tool", got)
	}
	kinds := func() []entryKind {
		var kinds []entryKind
		for _, entry := range view.transcript.entries {
			kinds = append(kinds, entry.kind)
		}
		return kinds
	}
	if got, want := fmt.Sprint(kinds()), fmt.Sprint([]entryKind{entryUser, entryAssistant, entryTool}); got != want {
		t.Fatalf("child entries = %v, want %v", got, want)
	}

	m = applyAgents(t, m, agentEnd("bg-1", errors.New("model unavailable"), 3*time.Second))
	view = m.agentView("bg-1")
	if view.running || view.end == nil || view.elapsed != 3*time.Second || view.transcript.status != "" {
		t.Fatalf("settled view = %#v", view)
	}
	entries := view.transcript.entries
	if tool := entries[2]; tool.toolState != toolFailed {
		t.Fatalf("a tool without an end is %v, want failed", tool.toolState)
	}
	if last := entries[len(entries)-1]; last.kind != entryError || last.text != "model unavailable" {
		t.Fatalf("last entry = %#v, want the error", last)
	}
}

// Only a start creates a view: events and ends of unknown children, and
// events after a run ended, change nothing. A continued child appends its
// next run to the same view.
func TestAgentUpdatesFollowRunBoundaries(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 80, 24),
		agentEvent("ghost", interaction.Event{Kind: interaction.EventAssistantStart}),
		agentEnd("ghost", nil, time.Second),
	)
	if len(m.agents) != 0 {
		t.Fatalf("unknown children made views: %#v", m.agents)
	}

	m = applyAgents(t, m,
		agentStart("bg-1", "first"),
		agentEnd("bg-1", nil, time.Second),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventAssistantStart}),
	)
	view := m.agentView("bg-1")
	if got := len(view.transcript.entries); got != 1 {
		t.Fatalf("an event after the end was applied: %d entries", got)
	}

	m = applyAgents(t, m, agentStart("bg-1", "and the tests?"))
	view = m.agentView("bg-1")
	if len(m.agents) != 1 || !view.running || view.end != nil {
		t.Fatalf("continued child = %#v among %d views", view, len(m.agents))
	}
	m = applyAgents(t, m, agentEnd("bg-1", context.Canceled, 2*time.Second))
	view = m.agentView("bg-1")
	entries := view.transcript.entries
	if view.elapsed != 3*time.Second || entries[1].text != "and the tests?" || entries[len(entries)-1].text != "Subagent stopped" {
		t.Fatalf("after two runs: elapsed %v, entries %#v", view.elapsed, entries)
	}
}

// Finished views are bounded; running ones always stay.
func TestAgentUpdatesKeepRecentFinishedChildren(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 80, 24), agentStart("running", "still going"))
	for i := range maximumFinishedAgents + 3 {
		id := fmt.Sprintf("bg-%d", i)
		m = applyAgents(t, m, agentStart(id, "task"), agentEnd(id, nil, time.Second))
	}
	if len(m.agents) != maximumFinishedAgents+1 || m.agentView("running") == nil {
		t.Fatalf("%d views kept", len(m.agents))
	}
	if m.agentView("bg-0") != nil || m.agentView(fmt.Sprintf("bg-%d", maximumFinishedAgents+2)) == nil {
		t.Fatal("pruning did not drop the oldest finished views")
	}
}

// A changed Session drops the previous Session's children.
func TestSessionChangeDropsAgentViews(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 80, 24), agentStart("bg-1", "task"))
	state := interaction.RuntimeState{SessionChanged: true}
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{state: &state}}})
	if len(m.agents) != 0 {
		t.Fatalf("views after a Session change: %#v", m.agents)
	}
}

// Updates arrive in batches; a closed channel ends the wait for more.
func TestWaitForAgentUpdatesBatches(t *testing.T) {
	t.Parallel()
	updates := make(chan interaction.AgentUpdate, 4)
	updates <- agentStart("bg-1", "task")
	batch := waitForAgentUpdates(updates)().(agentUpdatesMsg)
	if len(batch.updates) != 1 || batch.closed {
		t.Fatalf("lifecycle batch = %#v", batch)
	}

	delta := interaction.Event{Kind: interaction.EventAssistantDelta}
	updates <- agentEvent("bg-1", delta)
	updates <- agentEvent("bg-1", delta)
	close(updates)
	batch = waitForAgentUpdates(updates)().(agentUpdatesMsg)
	if len(batch.updates) != 2 || !batch.closed {
		t.Fatalf("streamed batch = %#v", batch)
	}

	m := testModel(t, 80, 24)
	m.agentUpdates = updates
	next, command := m.applyAgentUpdates(agentUpdatesMsg{closed: true})
	if next.(model).agentUpdates != nil || command != nil {
		t.Fatal("a closed channel is still awaited")
	}
}
