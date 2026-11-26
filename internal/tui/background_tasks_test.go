package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// fakeTasks is a task source a test edits between polls.
type fakeTasks struct {
	running []interaction.TaskInfo
	stopped []string
	output  string
}

func (f *fakeTasks) Tasks() []interaction.TaskInfo { return f.running }

func (f *fakeTasks) StopTask(id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeTasks) TaskOutput(string) (string, error) { return f.output, nil }

func (*fakeTasks) AwaitCompletion(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func shellTask(id, command string) interaction.TaskInfo {
	return interaction.TaskInfo{ID: id, Kind: interaction.TaskShell, Command: command, Started: time.Now()}
}

// The composer shows running background tasks. A count a run reports that
// the list does not match fetches the list at once; then exactly one poll
// stays pending until none remain, including tasks that end between runs.
func TestBackgroundTaskCountFollowsTasks(t *testing.T) {
	t.Parallel()
	source := &fakeTasks{running: []interaction.TaskInfo{shellTask("bg-1", "sleep 9"), shellTask("bg-2", "sleep 8")}}
	current := newModel(nil, nil)
	current.currentModel = interaction.DisplayModel{ID: "deepseek-v4-flash"}
	current.pollTasks = backgroundTaskList(source)
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 100, Height: 24})
	shows := func(want string) bool { return strings.Contains(ansi.Strip(current.View().Content), want) }

	updated, command := current.applyRunBatch(runBatchMsg{updates: []runUpdate{{
		state: &interaction.RuntimeState{Model: current.currentModel, BackgroundTasks: 2},
	}}})
	current = updated.(model)
	if command == nil {
		t.Fatal("a changed count fetched no list")
	}
	current = updateModel(t, current, backgroundTasksMsg{tasks: source.Tasks()})
	if !shows("2 tasks · ") || !current.taskPollPending {
		t.Fatalf("count shown = %v, poll pending = %v", shows("2 tasks · "), current.taskPollPending)
	}

	source.running = source.running[:1]
	current = updateModel(t, current, backgroundTasksMsg{tasks: source.Tasks(), tick: true})
	if !shows("1 task · ") || !current.taskPollPending {
		t.Fatal("count not refreshed by the poll")
	}
	source.running = nil
	current = updateModel(t, current, backgroundTasksMsg{tasks: source.Tasks(), tick: true})
	if shows("task · ") || shows("tasks · ") || current.taskPollPending {
		t.Fatalf("finished tasks still shown or polled: pending = %v", current.taskPollPending)
	}
}

// A finished task wakes an idle view with a notification run. A busy view
// ignores the wake, and waits again once its run finishes.
func TestTaskCompletionWakesIdleView(t *testing.T) {
	t.Parallel()
	requests := make(chan runRequest, 1)
	current := newModel(requests, make(chan struct{}))
	waits := 0
	current.awaitTasks = func() tea.Cmd {
		waits++
		return func() tea.Msg { return nil }
	}

	updated, command := current.Update(taskCompletionMsg{})
	current = updated.(model)
	if !current.run.running() || command == nil {
		t.Fatalf("idle view did not start a run: running = %v", current.run.running())
	}
	if started, ok := command().(runStartedMsg); !ok || started.updates == nil {
		t.Fatal("wake did not reach the run controller")
	}
	if request := <-requests; !request.notification || request.prompt != "" {
		t.Fatalf("request = %+v, want a notification run", request)
	}

	updated, command = current.Update(taskCompletionMsg{})
	current = updated.(model)
	if command != nil || len(requests) != 0 || waits != 0 {
		t.Fatalf("busy view acted on a wake: command = %v, requests = %d, waits = %d", command != nil, len(requests), waits)
	}

	updated, _ = current.applyRunBatch(runBatchMsg{updates: []runUpdate{{done: true}}})
	current = updated.(model)
	if current.run.running() || !current.taskWaitPending || waits != 1 {
		t.Fatalf("finished run: running = %v, wait pending = %v, waits = %d", current.run.running(), current.taskWaitPending, waits)
	}

	// A failed notification run does not wait again; the next run's end does.
	current.taskWaitPending = false
	updated, command = current.Update(taskCompletionMsg{})
	current = updated.(model)
	if !current.run.running() || command == nil {
		t.Fatal("idle view did not wake again")
	}
	updated, _ = current.applyRunBatch(runBatchMsg{updates: []runUpdate{{done: true, err: errors.New("no credentials")}}})
	current = updated.(model)
	if current.taskWaitPending || waits != 1 {
		t.Fatalf("failed wake: wait pending = %v, waits = %d", current.taskWaitPending, waits)
	}

	// A wait that ended with the program does not start a run.
	current.taskWaitPending = false
	updated, command = current.Update(taskCompletionMsg{err: errors.New("stopped")})
	if updated.(model).run.running() || command != nil {
		t.Fatal("a failed wait started a run")
	}
}

// Running shell tasks list above the subagents below the composer. Down
// walks both sections as one list; x twice stops a task through the
// frontend, and Enter shows its output, read again while it runs.
func TestShellTasksInThePanel(t *testing.T) {
	t.Parallel()
	source := &fakeTasks{running: []interaction.TaskInfo{
		shellTask("bg-1", "npm run dev"),
		{ID: "bg-9", Kind: interaction.TaskSubagent, Description: "SUBAGENT TASK", Started: time.Now()},
	}, output: "listening on :3000\n"}
	m := testModel(t, 100, 30)
	m.pollTasks, m.stopTask, m.readTaskOutput = backgroundTaskList(source), taskStopCommand(source), taskOutputCommand(source)
	m = updateModel(t, m, backgroundTasksMsg{tasks: source.Tasks()})
	m = applyAgents(t, m, agentStart("bg-9", "task"))
	summary := ansiStripped(m)
	if !strings.Contains(summary, "1 shell · 1 subagent") || !strings.Contains(summary, "npm run dev") || strings.Contains(summary, "SUBAGENT TASK") || !strings.Contains(summary, "↓ tasks") {
		t.Fatalf("summary bar lacks the shell task and subagent, or lists the subagent task twice:\n%s", summary)
	}

	m = press(t, m, "down")
	if m.inputContext().domain != inputPanel || m.panelSelected != "bg-1" {
		t.Fatalf("Down selected %q in %v", m.panelSelected, m.inputContext().domain)
	}
	view := ansiStripped(m)
	tasks, agents := strings.Index(view, "Shell tasks · 1"), strings.Index(view, "Subagents · 1 running / 1")
	if tasks < 0 || agents < tasks || !strings.Contains(view, "npm run dev") || strings.Contains(view, "SUBAGENT TASK") {
		t.Fatalf("expanded drawer lacks the shell task above the subagent, or lists the subagent task twice:\n%s", view)
	}
	m = press(t, m, "down")
	if m.panelSelected != "bg-9" {
		t.Fatalf("Down did not cross into the subagents: %q", m.panelSelected)
	}
	m = press(t, m, "up")
	m = press(t, m, "x")
	next, stop := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(model)
	if stop == nil {
		t.Fatal("x twice returned no stop")
	}
	m = updateModel(t, m, stop())
	if len(source.stopped) != 1 || source.stopped[0] != "bg-1" {
		t.Fatalf("stopped %v", source.stopped)
	}

	reader, command, _ := m.handlePanelAction(inputActionMatch{action: inputActionPanelOpen, matched: true, enabled: true})
	if reader.reading == nil || reader.reading.task == nil || command == nil {
		t.Fatal("Enter did not open the task's output")
	}
	m = updateModel(t, reader, command())
	if view := ansiStripped(m); !strings.Contains(view, "SHELL TASK · npm run dev · running") || !strings.Contains(view, "listening on :3000") {
		t.Fatalf("output view:\n%s", view)
	}
	m = press(t, m, "esc")
	if m.reading != nil || m.inputContext().domain != inputPanel {
		t.Fatalf("closing the output left reading %v in %v", m.reading, m.inputContext().domain)
	}
}
