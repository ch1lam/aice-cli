package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// backgroundTaskPollInterval paces the count while tasks run between runs;
// no poll is scheduled once none remain.
const backgroundTaskPollInterval = 2 * time.Second

type backgroundTasksMsg struct{ running int }

func backgroundTaskPoll(counter interaction.BackgroundTaskCounter) func() tea.Cmd {
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
