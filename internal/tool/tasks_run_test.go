package tool_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func newRunTasks(t *testing.T) (*tool.Tasks, *tool.TaskOutput, *tool.TaskKill) {
	t.Helper()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tasks.Close(); err != nil {
			t.Error(err)
		}
	})
	output, err := tool.NewTaskOutput(tasks)
	if err != nil {
		t.Fatal(err)
	}
	kill, err := tool.NewTaskKill(tasks)
	if err != nil {
		t.Fatal(err)
	}
	return tasks, output, kill
}

// A run task's report reaches task_output from the start of its file, and
// its delegation is recorded only by the result that reports it first.
func TestRunTaskReportsDelegationOnce(t *testing.T) {
	t.Parallel()
	tasks, output, _ := newRunTasks(t)
	status, err := tasks.StartRun(tool.TaskAgent, "find loader", nil, func(_ context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
		fmt.Fprintln(out, "the loader is config.Load")
		return &llm.ToolDelegation{SessionID: "child", Usage: llm.Usage{TotalTokens: 7}, Turns: 2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	call := toolCall(t, "task_output", map[string]any{"task_ids": []string{status.ID}, "wait": 10})
	first, err := output.Execute(t.Context(), call)
	if err != nil {
		t.Fatal(err)
	}
	text := first.Content[0].Text
	if !strings.Contains(text, "subagent task "+status.ID+" finished") || !strings.Contains(text, "the loader is config.Load") ||
		first.Details == nil || len(first.Details.Delegations) != 1 || first.Details.Delegations[0].Usage.TotalTokens != 7 {
		t.Fatalf("first report = %q / %#v", text, first.Details)
	}
	again, err := output.Execute(t.Context(), call)
	if err != nil || again.Details != nil {
		t.Fatalf("second report = %#v, %v; usage must be recorded once", again.Details, err)
	}
	if _, _, ok := tasks.TakeNotice(); ok {
		t.Fatal("task_output already reported it, yet a notice followed")
	}
}

// A completion nobody waited for arrives as a notice carrying the record and
// the whole report, which is longer than a command's preview.
func TestRunTaskCompletionNoticeCarriesDelegation(t *testing.T) {
	t.Parallel()
	tasks, _, _ := newRunTasks(t)
	report := strings.Repeat("evidence ", 1000) + "conclusion"
	changed := tasks.Changed()
	if _, err := tasks.StartRun(tool.TaskAgent, "probe", nil, func(_ context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
		fmt.Fprintln(out, report)
		return &llm.ToolDelegation{SessionID: "child"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(10 * time.Second):
		t.Fatal("no change signal for the finished subagent")
	}
	notice, _, ok := tasks.TakeNotice()
	if !ok || notice.Notice == nil || len(notice.Notice.Delegations) != 1 || notice.Notice.Delegations[0].SessionID != "child" ||
		!strings.Contains(notice.Content[0].Text, report) {
		t.Fatalf("notice = %#v", notice)
	}
	if err := notice.Validate(); err != nil {
		t.Fatal(err)
	}
}

// task_kill cancels a run task's context and reports how it ended; a run
// that fails on its own reports its error.
func TestRunTaskKillAndFailure(t *testing.T) {
	t.Parallel()
	tasks, output, kill := newRunTasks(t)
	blocked, err := tasks.StartRun(tool.TaskAgent, "wait", nil, func(ctx context.Context, _ string, _ io.Writer) (*llm.ToolDelegation, error) {
		<-ctx.Done()
		return &llm.ToolDelegation{SessionID: "stopped"}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := kill.Execute(t.Context(), toolCall(t, "task_kill", map[string]any{"task_id": blocked.ID}))
	if err != nil || !strings.Contains(result.Content[0].Text, "terminated: killed by task_kill") ||
		result.Details == nil || len(result.Details.Delegations) != 1 {
		t.Fatalf("kill result = %#v, %v", result, err)
	}
	failed, err := tasks.StartRun(tool.TaskAgent, "fail", nil, func(context.Context, string, io.Writer) (*llm.ToolDelegation, error) {
		return nil, fmt.Errorf("model unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := output.Execute(t.Context(), toolCall(t, "task_output", map[string]any{"task_ids": []string{failed.ID}, "wait": 10}))
	if err != nil || !strings.Contains(report.Content[0].Text, "stopped before finishing: model unavailable") || report.Details != nil {
		t.Fatalf("failed report = %#v, %v", report, err)
	}
}

func TestRunTasksBoundConcurrentSubagents(t *testing.T) {
	t.Parallel()
	tasks, _, _ := newRunTasks(t)
	block := func(ctx context.Context, _ string, _ io.Writer) (*llm.ToolDelegation, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	for range 4 {
		if _, err := tasks.StartRun(tool.TaskAgent, "wait", nil, block); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tasks.StartRun(tool.TaskAgent, "fifth", nil, block); err == nil || !strings.Contains(err.Error(), "at most 4 background subagents") {
		t.Fatalf("fifth subagent err = %v", err)
	}
}

// A finished run task continues under its ID and owner, rewriting its
// output. A completion nobody reported comes back with the continuation, so
// its report and usage are not lost; a task stopped with task_kill is closed.
func TestRunTaskContinues(t *testing.T) {
	t.Parallel()
	tasks, output, kill := newRunTasks(t)
	owner := new(int)
	report := func(text string, tokens int64) tool.TaskRun {
		return func(_ context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
			fmt.Fprintln(out, text)
			return &llm.ToolDelegation{SessionID: "child", Usage: llm.Usage{TotalTokens: tokens}}, nil
		}
	}
	status, err := tasks.StartRun(tool.TaskAgent, "probe", owner, report("first report", 3))
	if err != nil {
		t.Fatal(err)
	}
	done, err := tasks.Done(status.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if got, err := tasks.Owner(status.ID); err != nil || got != owner {
		t.Fatalf("owner = %v, %v", got, err)
	}
	if list := tasks.List(); len(list) != 1 || list[0].Running || !tasks.HasCompletions() {
		t.Fatalf("list = %+v; listing must not report the completion", list)
	}

	continued, err := tasks.Continue(status.ID, report("second report", 5))
	if err != nil {
		t.Fatal(err)
	}
	if continued.Task.ID != status.ID || continued.Unseen == nil || continued.Unseen.Delegation.Usage.TotalTokens != 3 ||
		!strings.Contains(continued.UnseenReport, "first report") || tasks.HasCompletions() {
		t.Fatalf("continuation = %+v", continued)
	}
	result, err := output.Execute(t.Context(), toolCall(t, "task_output", map[string]any{"task_ids": []string{status.ID}, "wait": 10}))
	if err != nil {
		t.Fatal(err)
	}
	if text := result.Content[0].Text; !strings.Contains(text, "second report") || strings.Contains(text, "first report") ||
		len(result.Details.Delegations) != 1 || result.Details.Delegations[0].Usage.TotalTokens != 5 {
		t.Fatalf("second run = %q / %#v", text, result.Details)
	}
	if again, err := tasks.Continue(status.ID, report("third report", 1)); err != nil || again.Unseen != nil {
		t.Fatalf("a reported run comes back unseen: %+v, %v", again, err)
	}

	if _, err := kill.Execute(t.Context(), toolCall(t, "task_kill", map[string]any{"task_id": status.ID})); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Continue(status.ID, report("closed", 0)); !errors.Is(err, tool.ErrTaskClosed) {
		t.Fatalf("continuing after task_kill: %v", err)
	}
}

// Without task IDs, task_output lists every task and reports none of them.
func TestTaskOutputListsTasks(t *testing.T) {
	t.Parallel()
	tasks, output, _ := newRunTasks(t)
	status, err := tasks.StartRun(tool.TaskAgent, "probe", nil, func(context.Context, string, io.Writer) (*llm.ToolDelegation, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	done, _ := tasks.Done(status.ID)
	<-done
	result, err := output.Execute(t.Context(), toolCall(t, "task_output", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if text := result.Content[0].Text; !strings.Contains(text, "Subagent "+status.ID+" finished: probe") || !strings.Contains(text, "send_message") ||
		result.Details != nil || !tasks.HasCompletions() {
		t.Fatalf("list = %q / %#v", text, result.Details)
	}
}
