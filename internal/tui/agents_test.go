package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// Running children show below the composer with what they do, their model
// and how long they ran; the transcript yields the rows they take.
func TestAgentPanelShowsRunningChildren(t *testing.T) {
	t.Parallel()
	base := testModel(t, 100, 30)
	m := applyAgents(t, base,
		agentStart("bg-1", "task"),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolStart, Tool: interaction.ToolDisplay{ID: "t1", Name: "read", Detail: "internal/agent/loop.go"}}),
		agentStart("bg-2", "task"),
		agentStart("bg-3", "task"),
		agentEnd("bg-3", nil, time.Second),
	)
	m = applyAgents(t, m,
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolEnd, Tool: interaction.ToolDisplay{ID: "t1", Name: "read"}}),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolStart, Tool: interaction.ToolDisplay{ID: "t2", Name: "read", Detail: "internal/agent/loop.go"}}),
	)
	view := ansiStripped(m)
	for _, want := range []string{"Subagents 2", "find loader · Running read internal/agent/loop.go · 1 tool", "child-model 0s", "find loader · Starting"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	if m.chrome.agents != 3 || m.viewport.Height() != base.viewport.Height()-3 {
		t.Fatalf("panel takes %d rows, viewport %d (was %d)", m.chrome.agents, m.viewport.Height(), base.viewport.Height())
	}

	m = applyAgents(t, m, agentEnd("bg-1", nil, time.Second), agentEnd("bg-2", nil, time.Second))
	if view := ansiStripped(m); strings.Contains(view, "Subagents") || m.chrome.agents != 0 || m.viewport.Height() != base.viewport.Height() {
		t.Fatalf("panel stays without running children (%d rows):\n%s", m.chrome.agents, view)
	}
}

// The panel shows at most a few rows and counts the rest.
func TestAgentPanelCountsChildrenBeyondItsRows(t *testing.T) {
	t.Parallel()
	m := testModel(t, 100, 30)
	for i := range maximumAgentRows + 2 {
		m = applyAgents(t, m, agentStart(fmt.Sprintf("bg-%d", i), "task"))
	}
	view := ansiStripped(m)
	if !strings.Contains(view, "Subagents 7") || !strings.Contains(view, "+3 more") || m.chrome.agents != maximumAgentRows+1 {
		t.Fatalf("panel of %d rows:\n%s", m.chrome.agents, view)
	}
}

// The agent call that started a child shows what the child does and, once
// it ended, how it ended.
func TestAgentCallShowsItsChild(t *testing.T) {
	t.Parallel()
	m := testModel(t, 100, 30)
	m.entries = []transcriptEntry{{kind: entryTool, toolID: "call-bg-1", toolName: "agent", toolDetail: "find loader", toolState: toolSucceeded}}
	m = applyAgents(t, m,
		agentStart("bg-1", "task"),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolStart, Tool: interaction.ToolDisplay{ID: "t1", Name: "bash", Detail: "go test ./...", Description: "run the tests"}}),
	)
	if view := m.transcriptView(); !strings.Contains(view, "↳ running · Running bash run the tests") {
		t.Fatalf("running call:\n%s", view)
	}
	m = applyAgents(t, m, agentEnd("bg-1", nil, 3*time.Second))
	if view := m.transcriptView(); !strings.Contains(view, "↳ completed in 3s · 1 turn · 1 tool call") {
		t.Fatalf("finished call:\n%s", view)
	}

	m = applyAgents(t, m, agentStart("bg-1", "more"), agentEnd("bg-1", errors.New("quota exceeded"), time.Second))
	if view := m.transcriptView(); !strings.Contains(view, "↳ failed: quota exceeded") {
		t.Fatalf("failed call:\n%s", view)
	}
}

func ansiStripped(m model) string {
	return ansi.Strip(m.View().Content)
}

func press(t *testing.T, m model, text string) model {
	t.Helper()
	switch text {
	case "up":
		return updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	case "down":
		return updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	case "esc":
		return updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	case "enter":
		return updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	return updateModel(t, m, tea.KeyPressMsg{Code: rune(text[0]), Text: text})
}

// Down past the newest prompt moves the keyboard to the panel below the
// composer, on its top row; the panel then lists finished children too.
// Arrows select by ID, so pruning does not move the selection, and Up from
// the top row returns the keyboard to the composer.
func TestAgentPanelFocusSelectsChildren(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 100, 30),
		agentStart("bg-1", "task"), agentEnd("bg-1", nil, 2*time.Second),
		agentStart("bg-2", "task"),
		agentStart("bg-3", "task"), agentEnd("bg-3", errors.New("quota exceeded"), time.Second),
	)
	m = press(t, m, "down")
	if c := m.inputContext(); c.domain != inputAgents || c.editor || m.agentSelected != "bg-1" {
		t.Fatalf("focus = %#v on %q, want the top row", c, m.agentSelected)
	}
	view := ansiStripped(m)
	for _, want := range []string{"Subagents 1 running · 3", "› ✓ find loader · completed in 2s", "⠋ find loader · Starting", "✗ find loader · failed: quota exceeded"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	if composer, panel := strings.Index(view, "╰"), strings.Index(view, "Subagents"); composer < 0 || panel < composer {
		t.Fatalf("panel is not below the composer:\n%s", view)
	}
	m = press(t, m, "down")
	m = press(t, m, "down")
	m = press(t, m, "down")
	if m.agentSelected != "bg-3" {
		t.Fatalf("selected %q after moving past the end", m.agentSelected)
	}
	m = press(t, m, "up")
	m = press(t, m, "up")
	if m.agentSelected != "bg-1" || m.inputContext().domain != inputAgents {
		t.Fatalf("selected %q, want the first child", m.agentSelected)
	}
	m = press(t, m, "up")
	if m.inputContext().domain != inputMain || strings.Contains(ansiStripped(m), "running · 3") {
		t.Fatalf("focus stays on the panel: %#v", m.inputContext())
	}

	m = press(t, m, "down")
	m = press(t, m, "esc")
	if m.inputContext().domain != inputMain {
		t.Fatalf("Esc kept the panel: %#v", m.inputContext())
	}
}

// Typing in the panel returns to the composer, which receives the text.
func TestAgentPanelTypingReturnsToTheComposer(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 100, 30), agentStart("bg-1", "task"))
	m = press(t, m, "down")
	m = press(t, m, "q")
	if m.inputContext().domain != inputMain || m.input.Value() != "q" {
		t.Fatalf("domain %v, composer %q", m.inputContext().domain, m.input.Value())
	}
}

// Down walks recalled prompts back to the draft first, and leaves a
// multi-line draft row by row, before it reaches the panel.
func TestAgentPanelFollowsHistoryAndDraft(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 100, 30), agentStart("bg-1", "task"))
	m.promptHistory = []string{"older"}
	m = press(t, m, "up")
	m = press(t, m, "down")
	if m.inputContext().domain != inputMain || m.historyIndex != -1 {
		t.Fatalf("Down left history for the panel: domain %v, index %d", m.inputContext().domain, m.historyIndex)
	}
	m.input.SetValue("one\ntwo")
	m.input.MoveToBegin()
	m = press(t, m, "down")
	if m.inputContext().domain != inputMain || m.input.Line() != 1 {
		t.Fatalf("Down left a draft row for the panel: domain %v, line %d", m.inputContext().domain, m.input.Line())
	}
	m = press(t, m, "down")
	if m.inputContext().domain != inputAgents {
		t.Fatalf("Down on the last row kept the composer: %v", m.inputContext().domain)
	}
}

// Without children, Down is not taken from the composer.
func TestAgentPanelFocusNeedsChildren(t *testing.T) {
	t.Parallel()
	m := press(t, testModel(t, 100, 30), "down")
	if m.inputContext().domain != inputMain || m.agentFocus {
		t.Fatalf("focus = %#v", m.inputContext())
	}
}

// x pressed twice stops a running background child through the frontend; a
// synchronous one stops with its response, so x does nothing for it. A
// failed stop is shown.
func TestAgentPanelStopsBackgroundChildren(t *testing.T) {
	t.Parallel()
	background := agentStart("bg-1", "task")
	background.Start.Background = true
	m := applyAgents(t, testModel(t, 100, 30), agentStart("agent-abc123", "task"), background)
	var stopped []string
	m.stopAgent = func(id string) tea.Cmd {
		stopped = append(stopped, id)
		return nil
	}
	m = press(t, m, "down")
	m = press(t, m, "down")
	m = press(t, m, "x")
	if len(stopped) != 0 || !strings.Contains(ansiStripped(m), "x again to stop") {
		t.Fatalf("one x stopped %v", stopped)
	}
	m = press(t, m, "up")
	m = press(t, m, "down")
	m = press(t, m, "x")
	if len(stopped) != 0 {
		t.Fatalf("moving did not disarm the stop: %v", stopped)
	}
	m = press(t, m, "x")
	if len(stopped) != 1 || stopped[0] != "bg-1" || m.agentView("bg-1").activity != "Stopping" {
		t.Fatalf("stopped %v", stopped)
	}
	m = updateModel(t, m, agentStopMsg{id: "bg-1", err: errors.New("no such task")})
	if last := m.entries[len(m.entries)-1]; last.kind != entryError || !strings.Contains(last.text, "no such task") {
		t.Fatalf("a failed stop is not shown: %#v", last)
	}
	m = press(t, m, "up")
	m = press(t, m, "x")
	if len(stopped) != 1 {
		t.Fatalf("a synchronous child was stopped: %v", stopped)
	}
}

// Enter opens the selected child's transcript, which follows the child live
// while the main model keeps its state; Esc returns to the panel.
func TestAgentReaderFollowsTheChild(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 100, 30),
		agentStart("bg-1", "find the config loader"),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventAssistantEnd, Assistant: interaction.AssistantDisplay{Text: "looking around"}}),
	)
	m = press(t, m, "down")
	m = press(t, m, "enter")
	if m.reading == nil || m.reading.agent != "bg-1" {
		t.Fatalf("reading = %#v", m.reading)
	}
	view := ansiStripped(m)
	for _, want := range []string{"SUBAGENT · find loader · child-model", "find the config loader", "looking around", "Enter back to subagents"} {
		if !strings.Contains(view, want) {
			t.Fatalf("reader lacks %q:\n%s", want, view)
		}
	}
	m = applyAgents(t, m,
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventAssistantEnd, Assistant: interaction.AssistantDisplay{Text: "the loader is config.Load"}}),
		agentEnd("bg-1", nil, 4*time.Second),
	)
	if view := ansiStripped(m); !strings.Contains(view, "the loader is config.Load") || !strings.Contains(view, "completed in 4s") {
		t.Fatalf("reader did not follow the child:\n%s", view)
	}
	if len(m.reading.previous.entries) != 0 {
		t.Fatalf("the child changed the main transcript: %#v", m.reading.previous.entries)
	}
	m = press(t, m, "esc")
	if m.reading != nil || m.inputContext().domain != inputAgents {
		t.Fatalf("after closing: reading %v, domain %v", m.reading, m.inputContext().domain)
	}
}

// A run that waits for its background subagent says so, counts the child's
// tool calls in the process header, and times the response to the child's
// report from the person's prompt, wait included.
func TestRunWaitingForSubagentsKeepsOneTurn(t *testing.T) {
	t.Parallel()
	m := testModel(t, 100, 24)
	m.run.kind = runPrompt
	turnStart := time.Now().Add(-20 * time.Second)
	m.entries = []transcriptEntry{{kind: entryUser, text: "survey the repo"}}
	m.beginProcess()
	m.processGroup(m.activeProcessID).startedAt = turnStart
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventAssistantStart})
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventToolStart, Tool: interaction.ToolDisplay{ID: "call-bg-1", Name: "agent"}})
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventToolEnd, Tool: interaction.ToolDisplay{ID: "call-bg-1", Name: "agent"}})
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventAssistantStart})
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventAssistantEnd, Assistant: interaction.AssistantDisplay{Text: "waiting for the survey", Concludes: true}})
	first := m.activeProcessID
	m = applyAgents(t, m,
		agentStart("bg-1", "survey"),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolEnd, Tool: interaction.ToolDisplay{ID: "t1", Name: "ls"}}),
		agentEvent("bg-1", interaction.Event{Kind: interaction.EventToolEnd, Tool: interaction.ToolDisplay{ID: "t2", Name: "grep"}}),
	)

	if got := ansi.Strip(m.activityIndicator()); !strings.Contains(got, "Waiting for 1 subagent...") {
		t.Fatalf("activity = %q, want the wait for the subagent", got)
	}
	if got := ansi.Strip(m.processHeader(1, len(m.entries), true)); !strings.Contains(got, "1 tool call · 1 subagent (2 tool calls)") {
		t.Fatalf("process header = %q, want the subagent's tool calls", got)
	}

	m = applyAgents(t, m, agentEnd("bg-1", nil, 15*time.Second))
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventNotification, Input: interaction.InputDisplay{ID: "notice-bg-1", Text: "bg-1 completed"}})
	if m.activeProcessID == first {
		t.Fatal("the response to the report did not start a new process")
	}
	if got := m.processGroup(m.activeProcessID).startedAt; !got.Equal(turnStart) {
		t.Fatalf("response process started at %v, want the turn's start %v", got, turnStart)
	}
	if elapsed, _ := m.processDuration(m.activeProcessID); elapsed < 20*time.Second {
		t.Fatalf("response process elapsed = %s, want the whole turn", elapsed)
	}
}

// Session totals an event carries update the status line during the run.
func TestRunEventsUpdateSessionTotals(t *testing.T) {
	t.Parallel()
	m := testModel(t, 100, 24)
	m.run.kind = runPrompt
	usage := interaction.DisplayUsage{InputTokens: 1200, OutputTokens: 80}
	throughput := interaction.DisplayThroughput{OutputTokens: 80, Duration: time.Second}
	m.applyAgentEvent(interaction.Event{Kind: interaction.EventToolEnd, Tool: interaction.ToolDisplay{ID: "t1", Name: "agent"}, Usage: &usage, Throughput: &throughput})
	if m.sessionUsage != usage || m.throughput != throughput {
		t.Fatalf("totals = %+v / %+v, want the event's", m.sessionUsage, m.throughput)
	}
}

// Hovering a row marks it without taking the keyboard; a click opens that
// child's transcript as Enter does, and closing it returns to the panel.
func TestAgentPanelPointer(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 100, 30), agentStart("bg-1", "task"), agentStart("bg-2", "second task"))
	area := m.screenLayout().agents
	row := tea.Mouse{X: area.x + 4, Y: area.y + 2, Button: tea.MouseLeft}
	m = updateModel(t, m, tea.MouseMotionMsg(row))
	lines := strings.Split(ansiStripped(m), "\n")
	if line := lines[row.Y]; !strings.Contains(line, "› ⠋ find loader") || m.inputContext().domain != inputMain {
		t.Fatalf("hover row %q, domain %v", line, m.inputContext().domain)
	}

	m = updateModel(t, m, tea.MouseClickMsg(row))
	m = updateModel(t, m, tea.MouseReleaseMsg(row))
	if m.reading == nil || m.reading.agent != "bg-2" {
		t.Fatalf("click opened %#v", m.reading)
	}
	m = press(t, m, "esc")
	if m.inputContext().domain != inputAgents || m.agentSelected != "bg-2" {
		t.Fatalf("after closing: domain %v on %q", m.inputContext().domain, m.agentSelected)
	}

	// A press that leaves its row opens nothing.
	m = press(t, m, "esc")
	area = m.screenLayout().agents
	first := tea.Mouse{X: area.x + 4, Y: area.y + 1, Button: tea.MouseLeft}
	m = updateModel(t, m, tea.MouseClickMsg(first))
	moved := first
	moved.Y++
	m = updateModel(t, m, tea.MouseMotionMsg(moved))
	m = updateModel(t, m, tea.MouseReleaseMsg(moved))
	if m.reading != nil {
		t.Fatal("a dragged press opened a child")
	}
}

// footerHint is the screen cell of a footer hint's first character.
func footerHint(t *testing.T, m model, hint string) tea.Mouse {
	t.Helper()
	y := m.screenLayout().footer.y
	line := strings.Split(ansiStripped(m), "\n")[y]
	i := strings.Index(line, hint)
	if i < 0 {
		t.Fatalf("footer lacks %q: %q", hint, line)
	}
	return tea.Mouse{X: ansi.StringWidth(line[:i]), Y: y, Button: tea.MouseLeft}
}

func clickAt(t *testing.T, m model, mouse tea.Mouse) model {
	t.Helper()
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	return updateModel(t, m, tea.MouseReleaseMsg(mouse))
}

// A footer hint underlines under the pointer and runs its action when
// clicked, as its key does, in whichever domain shows it.
func TestFooterHintsAreClickable(t *testing.T) {
	t.Parallel()
	m := applyAgents(t, testModel(t, 100, 30), agentStart("bg-1", "task"))
	hint := footerHint(t, m, "↓ subagents")
	plain := m.View().Content
	hovered := updateModel(t, m, tea.MouseMotionMsg(hint))
	if hovered.View().Content == plain || ansiStripped(hovered) != ansi.Strip(plain) {
		t.Fatal("hovering a hint changed nothing but its style")
	}
	m = clickAt(t, hovered, hint)
	if m.inputContext().domain != inputAgents {
		t.Fatalf("clicking the hint left domain %v", m.inputContext().domain)
	}
	m = clickAt(t, m, footerHint(t, m, "Esc back"))
	if m.inputContext().domain != inputMain {
		t.Fatalf("clicking Esc back left domain %v", m.inputContext().domain)
	}
}
