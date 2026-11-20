package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type taskCounter struct{ running int }

func (c *taskCounter) RunningTasks() int { return c.running }

// The composer shows running background tasks and keeps exactly one poll
// pending until none remain, including tasks that end between runs.
func TestBackgroundTaskCountFollowsTasks(t *testing.T) {
	t.Parallel()
	counter := &taskCounter{running: 2}
	current := newModel(nil, nil)
	current.currentModel = interaction.DisplayModel{ID: "deepseek-v4-flash"}
	current.pollTasks = backgroundTaskPoll(counter)
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 100, Height: 24})
	shows := func(want string) bool { return strings.Contains(ansi.Strip(current.View().Content), want) }

	updated, command := current.applyRunBatch(runBatchMsg{updates: []runUpdate{{
		state: &interaction.RuntimeState{Model: current.currentModel, BackgroundTasks: 2},
	}}})
	current = updated.(model)
	if !shows("2 tasks · ") || command == nil || !current.taskPollPending {
		t.Fatalf("count shown = %v, poll pending = %v", shows("2 tasks · "), current.taskPollPending)
	}
	updated, _ = current.applyRunBatch(runBatchMsg{updates: []runUpdate{{
		state: &interaction.RuntimeState{Model: current.currentModel, BackgroundTasks: 1},
	}}})
	current = updated.(model)
	if !shows("1 task · ") {
		t.Fatal("count not refreshed by run state")
	}

	counter.running = 0
	current = updateModel(t, current, backgroundTasksMsg{running: counter.RunningTasks()})
	if shows("task · ") || shows("tasks · ") || current.taskPollPending {
		t.Fatalf("finished tasks still shown or polled: pending = %v", current.taskPollPending)
	}
}
