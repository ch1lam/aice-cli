package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// awaitSubagents waits while a subagent runs and returns its notification;
// with no subagent running it returns at once, even while another task such
// as a server keeps running, and cancellation ends the wait.
func TestAwaitSubagentsWaitsOnlyForSubagents(t *testing.T) {
	t.Parallel()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	await := awaitSubagents(tasks)

	if _, ok, err := await(t.Context()); ok || err != nil {
		t.Fatalf("await with nothing running = %v, %v", ok, err)
	}
	if _, err := tasks.StartRun(tool.TaskBash, "server", func(ctx context.Context, _ string, _ io.Writer) (*llm.ToolDelegation, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := await(t.Context()); ok || err != nil {
		t.Fatalf("await with only a command running = %v, %v", ok, err)
	}

	release := make(chan struct{})
	if _, err := tasks.StartRun(tool.TaskAgent, "probe", func(_ context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
		<-release
		_, err := io.WriteString(out, "found it")
		return &llm.ToolDelegation{SessionID: "child"}, err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, ok, err := await(ctx); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled await = %v, %v", ok, err)
	}
	close(release)
	input, ok, err := await(t.Context())
	if err != nil || !ok || !input.Message.IsNotice() || !strings.HasPrefix(input.ID, "notice-bg-") ||
		!strings.Contains(input.Message.Content[0].Text, "found it") || len(input.Message.Notice.Delegations) != 1 {
		t.Fatalf("await = %#v, %v, %v", input, ok, err)
	}
}
