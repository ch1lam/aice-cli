package tool_test

import (
	"context"
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
	status, err := tasks.StartRun(tool.TaskAgent, "find loader", func(_ context.Context, out io.Writer) (*llm.ToolDelegation, error) {
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
	if notices := tasks.CompletionNotices(); len(notices) != 0 {
		t.Fatalf("task_output already reported it; notices = %+v", notices)
	}
}

// A completion nobody waited for arrives as a notice carrying the record.
func TestRunTaskCompletionNoticeCarriesDelegation(t *testing.T) {
	t.Parallel()
	tasks, _, _ := newRunTasks(t)
	if _, err := tasks.StartRun(tool.TaskAgent, "probe", func(context.Context, io.Writer) (*llm.ToolDelegation, error) {
		return &llm.ToolDelegation{SessionID: "child"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if notices := tasks.CompletionNotices(); len(notices) > 0 {
			if len(notices) != 1 || notices[0].Delegation == nil || notices[0].Delegation.SessionID != "child" ||
				!strings.Contains(notices[0].Text, "(no report)") {
				t.Fatalf("notices = %+v", notices)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no completion notice")
}

// task_kill cancels a run task's context and reports how it ended; a run
// that fails on its own reports its error.
func TestRunTaskKillAndFailure(t *testing.T) {
	t.Parallel()
	tasks, output, kill := newRunTasks(t)
	blocked, err := tasks.StartRun(tool.TaskAgent, "wait", func(ctx context.Context, _ io.Writer) (*llm.ToolDelegation, error) {
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
	failed, err := tasks.StartRun(tool.TaskAgent, "fail", func(context.Context, io.Writer) (*llm.ToolDelegation, error) {
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
