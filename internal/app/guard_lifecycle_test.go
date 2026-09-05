package app

import (
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/guard"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
)

func TestInteractiveSessionNewResetsGrantsAfterDetach(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	gate, adapter, err := newExecutionGuard(workspace, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	store := createAppTestSession(t, filepath.Join(t.TempDir(), "session.jsonl"), workspace)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	runner := &interactiveSession{
		guard: gate, guardAdapter: adapter,
		conversation: conversationState{store: store},
		loop:         mustAppLoop(t, &recordingModel{response: "answer"}, nil),
		model:        deepseek.DefaultModel(),
	}
	call := llm.ToolCall{ID: "check", Name: "bash", Arguments: mustCommandArgs(t, "rm -rf ./scratch")}
	gate.AllowCommandSession("rm -rf ./scratch")
	check := func(want guard.Decision) {
		t.Helper()
		result, err := gate.Check(t.Context(), call)
		if err != nil || result.Decision != want {
			t.Fatalf("Check = %#v, error = %v, want %s", result, err, want)
		}
	}
	for range 2 {
		if err := runInteractive(t.Context(), runner, "question", nil); err != nil {
			t.Fatal(err)
		}
		check(guard.DecisionAllow)
	}
	runner.conversation.activeMainRun = &mainRunState{}
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "new"}); err == nil {
		t.Fatal("active /new succeeded")
	}
	check(guard.DecisionAllow)
	runner.conversation.activeMainRun = nil
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "new", Arguments: "invalid"}); err == nil {
		t.Fatal("invalid /new succeeded")
	}
	check(guard.DecisionAllow)
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "new"}); err != nil {
		t.Fatal(err)
	}
	check(guard.DecisionAsk)
	// The adapter continues using the same gate, including its yolo setting.
	adapter.yolo = true
	gate.AllowCommandSession("rm -rf ./scratch")
	if _, err := runner.RunSlashCommand(t.Context(), interaction.CommandRequest{Name: "new"}); err != nil {
		t.Fatal(err)
	}
	check(guard.DecisionAsk)
	result, err := adapter.Check(t.Context(), call)
	if err != nil || result.Decision != agent.GuardAllow {
		t.Fatalf("yolo after /new = %#v, error = %v", result, err)
	}
}
