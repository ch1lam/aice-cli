package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/tool"
)

var taskIDPattern = regexp.MustCompile(`bg-[0-9a-f]{6}`)

type taskTools struct {
	tasks  *tool.Tasks
	bash   *tool.Bash
	output *tool.TaskOutput
	kill   *tool.TaskKill
}

func newTaskTools(t *testing.T) taskTools {
	t.Helper()
	shell, err := deps.FindNativeBash(deps.DefaultOptions())
	if err != nil {
		t.Skipf("native Bash unavailable: %v", err)
	}
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tasks.Close(); err != nil {
			t.Error(err)
		}
	})
	workspace, _ := newWorkspace(t)
	bash, err := tool.NewBash(t.Context(), workspace, tool.BashShells{Native: shell}, tasks)
	if err != nil {
		t.Fatal(err)
	}
	output, err := tool.NewTaskOutput(tasks)
	if err != nil {
		t.Fatal(err)
	}
	kill, err := tool.NewTaskKill(tasks)
	if err != nil {
		t.Fatal(err)
	}
	return taskTools{tasks: tasks, bash: bash, output: output, kill: kill}
}

// start runs command in the background from a call context that is canceled
// right away: tasks outlive the call and run that started them.
func (tt taskTools) start(t *testing.T, command string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	result, err := tt.bash.Execute(ctx, toolCall(t, "bash", map[string]any{
		"command": command, "description": "test task", "run_in_background": true,
	}))
	cancel()
	if err != nil {
		t.Fatalf("start %q: %v", command, err)
	}
	id := taskIDPattern.FindString(resultText(t, result))
	if id == "" {
		t.Fatalf("no task id in %q", resultText(t, result))
	}
	return id
}

func (tt taskTools) wait(t *testing.T, wait float64, ids ...string) string {
	t.Helper()
	result, err := tt.output.Execute(t.Context(), toolCall(t, "task_output", map[string]any{
		"task_ids": ids, "wait": wait,
	}))
	if err != nil {
		t.Fatalf("task_output: %v", err)
	}
	return resultText(t, result)
}

func TestBackgroundTaskLifecycle(t *testing.T) {
	t.Parallel()
	tt := newTaskTools(t)

	started := time.Now()
	id := tt.start(t, "echo first; sleep 1; echo second; exit 3")
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("start blocked for %s", elapsed)
	}
	if report := tt.wait(t, 0, id); !strings.Contains(report, id+" running") {
		t.Fatalf("snapshot = %q, want running", report)
	}
	if notices := tt.tasks.CompletionNotices(); len(notices) != 0 {
		t.Fatalf("notices before completion = %+v", notices)
	}

	other := tt.start(t, "echo other")
	report := tt.wait(t, 30, id, other)
	if !strings.Contains(report, id+" exited with code 3") || !strings.Contains(report, "first\nsecond") ||
		!strings.Contains(report, other+" exited with code 0") {
		t.Fatalf("report = %q", report)
	}
	if notices := tt.tasks.CompletionNotices(); len(notices) != 0 {
		t.Fatalf("task_output already reported both tasks; notices = %+v", notices)
	}

	third := tt.start(t, "echo done")
	deadline := time.Now().Add(10 * time.Second)
	var notices []agent.ToolResultNotice
	for len(notices) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		notices = tt.tasks.CompletionNotices()
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Text, third+" exited with code 0") ||
		!strings.Contains(notices[0].Text, "done") || notices[0].Delegation != nil {
		t.Fatalf("notices = %+v", notices)
	}
	if again := tt.tasks.CompletionNotices(); len(again) != 0 {
		t.Fatalf("completion reported twice: %+v", again)
	}
}

func TestBackgroundTaskKill(t *testing.T) {
	t.Parallel()
	tt := newTaskTools(t)
	// The trap keeps the shell alive through SIGTERM, so only the kill after
	// the grace period ends it.
	id := tt.start(t, `trap '' TERM; echo ready; while :; do sleep 0.1; done`)
	tt.wait(t, 0.5, id)

	started := time.Now()
	result, err := tt.kill.Execute(t.Context(), toolCall(t, "task_kill", map[string]any{"task_id": id}))
	if err != nil {
		t.Fatal(err)
	}
	if report := resultText(t, result); !strings.Contains(report, "terminated: killed by task_kill") ||
		!strings.Contains(report, "ready") {
		t.Fatalf("kill report = %q", report)
	}
	if elapsed := time.Since(started); elapsed < time.Second || elapsed > 10*time.Second {
		t.Fatalf("kill took %s, want the grace period before the kill", elapsed)
	}
	if notices := tt.tasks.CompletionNotices(); len(notices) != 0 {
		t.Fatalf("killed task produced notices %+v", notices)
	}
	if _, err := tt.kill.Execute(t.Context(), toolCall(t, "task_kill", map[string]any{"task_id": id})); err != nil {
		t.Fatalf("killing a finished task: %v", err)
	}
}

func TestBackgroundTaskRejections(t *testing.T) {
	t.Parallel()
	tt := newTaskTools(t)
	_, err := tt.bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": "true", "description": "x", "run_in_background": true, "timeout": 5,
	}))
	if err == nil || !strings.Contains(err.Error(), "no timeout") {
		t.Fatalf("background with timeout error = %v", err)
	}
	_, err = tt.output.Execute(t.Context(), toolCall(t, "task_output", map[string]any{"task_ids": []string{"bg-000000"}}))
	if err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("unknown task error = %v", err)
	}

	var ids []string
	for range 8 {
		ids = append(ids, tt.start(t, "sleep 30"))
	}
	_, err = tt.bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": "true", "description": "x", "run_in_background": true,
	}))
	if err == nil || !strings.Contains(err.Error(), "at most 8") {
		t.Fatalf("ninth running task error = %v", err)
	}
}

func TestBackgroundTasksEndWithSessionAndClose(t *testing.T) {
	t.Parallel()
	tt := newTaskTools(t)
	id := tt.start(t, "sleep 30")
	report := tt.wait(t, 0, id)
	path := strings.TrimSuffix(regexp.MustCompile(`output file: (\S+)\]`).FindStringSubmatch(report)[1], "]")

	tt.tasks.KillAll()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output after KillAll: %v", err)
	}
	if notices := tt.tasks.CompletionNotices(); len(notices) != 0 {
		t.Fatalf("previous Session's tasks reported: %+v", notices)
	}
	if _, err := tt.output.Execute(t.Context(), toolCall(t, "task_output", map[string]any{"task_ids": []string{id}})); err == nil {
		t.Fatal("task survived KillAll")
	}

	tt.start(t, "sleep 30")
	dir := tt.tasks.Dir()
	started := time.Now()
	if err := tt.tasks.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Close took %s", elapsed)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("task directory after Close: %v", err)
	}
	if _, err := tt.bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": "true", "description": "x", "run_in_background": true,
	})); err == nil {
		t.Fatal("start after Close succeeded")
	}
}

func TestBashAdvertisesBackgroundOnlyWithTasks(t *testing.T) {
	t.Parallel()
	tt := newTaskTools(t)
	workspace, _ := newWorkspace(t)
	plain := newBash(t, workspace)
	for _, test := range []struct {
		bash *tool.Bash
		want bool
	}{{tt.bash, true}, {plain, false}} {
		definition := test.bash.Definition()
		if err := definition.Validate(); err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if _, got := schema.Properties["run_in_background"]; got != test.want {
			t.Fatalf("run_in_background advertised = %v, want %v", got, test.want)
		}
	}
}
