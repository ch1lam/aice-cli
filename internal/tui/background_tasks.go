package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// backgroundTaskPollInterval paces the task list while tasks run; no poll is
// scheduled once none remain. taskOutputInterval paces an open output view.
const (
	backgroundTaskPollInterval = 2 * time.Second
	taskOutputInterval         = time.Second
)

// backgroundTasksMsg is a snapshot of the running tasks; tick marks the
// scheduled poll, as opposed to a fetch a changed count asked for.
type backgroundTasksMsg struct {
	tasks []interaction.TaskInfo
	tick  bool
}

// backgroundTaskList fetches the task list after delay, or at once.
func backgroundTaskList(tasks interaction.BackgroundTasks) func(time.Duration) tea.Cmd {
	return func(delay time.Duration) tea.Cmd {
		if delay == 0 {
			return func() tea.Msg { return backgroundTasksMsg{tasks: tasks.Tasks()} }
		}
		return tea.Tick(delay, func(time.Time) tea.Msg { return backgroundTasksMsg{tasks: tasks.Tasks(), tick: true} })
	}
}

// followBackgroundTasks takes the running count a run reported: a count the
// list does not match fetches the list at once.
func (m *model) followBackgroundTasks(running int) tea.Cmd {
	if m.pollTasks == nil {
		return nil
	}
	if running != len(m.tasks) {
		return m.pollTasks(0)
	}
	return m.scheduleTaskPoll()
}

// scheduleTaskPoll keeps one poll pending while any task runs.
func (m *model) scheduleTaskPoll() tea.Cmd {
	if len(m.tasks) == 0 || m.pollTasks == nil || m.taskPollPending {
		return nil
	}
	m.taskPollPending = true
	return m.pollTasks(backgroundTaskPollInterval)
}

func (m model) applyBackgroundTasks(message backgroundTasksMsg) (tea.Model, tea.Cmd) {
	if message.tick {
		m.taskPollPending = false
	}
	shells := len(m.shellTasks())
	m.tasks = message.tasks
	if m.panelFocus && !m.panelFocused() {
		// The last row the keyboard was on ended.
		m.panelFocus, m.panelStopArmed = false, ""
	}
	var commands []tea.Cmd
	if now := len(m.shellTasks()); now != shells {
		m.resizeLayout()
		m.refreshViewport(false)
		if now > shells {
			// Animates the panel while no run does; extra tick chains collapse.
			commands = append(commands, m.spinner.Tick)
		}
	}
	return m, tea.Batch(append(commands, m.scheduleTaskPoll(), m.focusComposerIfIdle())...)
}

// focusComposerIfIdle gives the keyboard back to the composer when the panel
// it was on emptied.
func (m *model) focusComposerIfIdle() tea.Cmd {
	if m.panelFocus || m.input.Focused() || !m.composerInputEnabled() {
		return nil
	}
	return m.input.Focus()
}

// taskStopMsg reports a stop request; a failed one is shown.
type taskStopMsg struct {
	id  string
	err error
}

func taskStopCommand(tasks interaction.BackgroundTasks) func(string) tea.Cmd {
	return func(id string) tea.Cmd {
		return func() tea.Msg { return taskStopMsg{id: id, err: tasks.StopTask(id)} }
	}
}

func (m model) applyTaskStop(message taskStopMsg) (tea.Model, tea.Cmd) {
	if message.err == nil {
		// The list shows the task gone at once, not at the next poll.
		return m, m.pollTasks(0)
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryError, text: "Could not stop task " + message.id + ": " + message.err.Error()})
	m.refreshViewport(true)
	return m, nil
}

// taskOutputMsg is the end of one task's output, for the reader showing it.
type taskOutputMsg struct {
	id, text string
	err      error
}

func taskOutputCommand(tasks interaction.BackgroundTasks) func(string, time.Duration) tea.Cmd {
	return func(id string, delay time.Duration) tea.Cmd {
		read := func() tea.Msg {
			text, err := tasks.TaskOutput(id)
			return taskOutputMsg{id: id, text: text, err: err}
		}
		if delay == 0 {
			return read
		}
		return tea.Tick(delay, func(time.Time) tea.Msg { return read() })
	}
}

// openTask shows a shell task's output in the reader, which re-reads it while
// the task runs.
func (m model) openTask(task interaction.TaskInfo) (model, tea.Cmd) {
	reader := newModel(nil, nil)
	reader.width, reader.height = m.width, m.height
	reader.workingDirectory = m.workingDirectory
	reader.reading = &sessionReading{previous: &m, task: &task}
	reader.readTaskOutput = m.readTaskOutput
	reader.entries = []transcriptEntry{{kind: entryCommand, text: "Reading output…"}}
	reader.input.Blur()
	reader.resizeLayout()
	reader.refreshViewport(true)
	if reader.readTaskOutput == nil {
		return reader, nil
	}
	return reader, reader.readTaskOutput(task.ID, 0)
}

// applyTaskOutput shows the output in the reader following the task, staying
// at the bottom while it is there, and reads again while the task runs.
func (m model) applyTaskOutput(message taskOutputMsg) (tea.Model, tea.Cmd) {
	if m.reading == nil || m.reading.task == nil || m.reading.task.ID != message.id {
		// The reader closed or moved on.
		return m, nil
	}
	text := message.text
	if message.err != nil {
		text = "Output unavailable: " + message.err.Error()
	} else if strings.TrimSpace(text) == "" {
		text = "No output yet"
	}
	follow := m.viewport.AtBottom()
	m.entries = []transcriptEntry{{kind: entryCommand, text: sanitizeMultilineText(text)}}
	m.refreshViewport(follow)
	if message.err != nil || !m.taskRunning(message.id) {
		return m, nil
	}
	return m, m.readTaskOutput(message.id, taskOutputInterval)
}

// taskRunning reports whether the live model still lists the task.
func (m model) taskRunning(id string) bool {
	live := m.reading.previous
	return slices.ContainsFunc(live.tasks, func(task interaction.TaskInfo) bool { return task.ID == id })
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

// taskReadingLabel heads a reader that shows a shell task's output.
func (m model) taskReadingLabel() (string, bool) {
	if m.reading == nil || m.reading.task == nil || m.reading.previous == nil {
		return "", false
	}
	task := *m.reading.task
	name := sanitizeSingleLineText(task.Description)
	if name == "" {
		name = strings.Join(strings.Fields(sanitizeSingleLineText(task.Command)), " ")
	}
	if !m.taskRunning(task.ID) {
		return "SHELL TASK · " + name + " · ended", true
	}
	return m.spinner.View() + " SHELL TASK · " + name + " · running " + formatRunDuration(time.Since(task.Started).Truncate(time.Second)), true
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
