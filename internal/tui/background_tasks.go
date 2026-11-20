package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// backgroundTaskPollInterval paces the count while tasks run between runs;
// no poll is scheduled once none remain.
const backgroundTaskPollInterval = 2 * time.Second

type backgroundTasksMsg struct{ running int }

func backgroundTaskPoll(counter interaction.BackgroundTasks) func() tea.Cmd {
	return func() tea.Cmd {
		return tea.Tick(backgroundTaskPollInterval, func(time.Time) tea.Msg {
			return backgroundTasksMsg{running: counter.RunningTasks()}
		})
	}
}

// followBackgroundTasks records the running count and keeps one poll pending
// while any task runs.
func (m *model) followBackgroundTasks(running int) tea.Cmd {
	m.backgroundTasks = running
	if running == 0 || m.pollTasks == nil || m.taskPollPending {
		return nil
	}
	m.taskPollPending = true
	return m.pollTasks()
}

type taskCompletionMsg struct{ err error }

func taskCompletionWait(ctx context.Context, tasks interaction.BackgroundTasks) func() tea.Cmd {
	return func() tea.Cmd {
		return func() tea.Msg { return taskCompletionMsg{err: tasks.AwaitCompletion(ctx)} }
	}
}

// awaitTaskCompletion keeps one completion wait pending while the main view
// is idle. During a run the Loop takes completions itself; finishRun waits
// again for those that arrive after its last look.
func (m *model) awaitTaskCompletion() tea.Cmd {
	if m.awaitTasks == nil || m.taskWaitPending || m.run.running() || m.controllerClosed {
		return nil
	}
	m.taskWaitPending = true
	return m.awaitTasks()
}

// wakeForTasks starts a notification run for finished background tasks when
// the main view is idle; a busy view waits again once its run finishes.
func (m model) wakeForTasks(message taskCompletionMsg) (tea.Model, tea.Cmd) {
	m.taskWaitPending = false
	if message.err != nil || m.run.running() || m.controllerClosed {
		return m, nil
	}
	m.run = runState{kind: runPrompt, notification: true}
	m.pendingDeliveries = nil
	m.assistantEntry = -1
	return m, startRun(m.requests, m.controllerDone, interaction.RunInput{Notification: true})
}

func backgroundTasksLabel(running int) string {
	switch running {
	case 0:
		return ""
	case 1:
		return "1 task · "
	default:
		return fmt.Sprintf("%d tasks · ", running)
	}
}
