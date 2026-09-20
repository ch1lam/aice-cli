package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tui"
)

func TestInteractiveRunBudgetPersistsAfterCompaction(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "budget.jsonl")
	model, deps := longTaskDependencies(t, workspace)
	originalLoad := deps.loadConfig
	deps.loadConfig = func(options config.LoadOptions) (config.Config, error) {
		c, err := originalLoad(options)
		c.RunTokenBudget = 100_000
		return c, err
	}
	deps.runTUI = func(ctx context.Context, runner tui.Runner, _ tui.Options) error {
		active, err := runner.NewRun(ctx, interaction.RunInput{Prompt: longTaskGoal}, nil)
		if err != nil {
			return err
		}
		return active.Run(ctx)
	}
	command, err := newTestCommand(t, deps)
	if err != nil {
		t.Fatal(err)
	}
	command.SetArgs([]string{"--workspace", workspace, "--session", path})
	command.SetIn(strings.NewReader(""))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.ExecuteContext(t.Context()); !errors.Is(err, agent.ErrTokenBudget) {
		t.Fatalf("run error=%v", err)
	}
	snapshot := openSessionSnapshot(t, path)
	if model.summaryCalls == 0 || model.mainCalls >= 200 {
		t.Fatalf("requests/compactions=%d/%d", model.mainCalls, model.summaryCalls)
	}
	total := session.TotalUsage(snapshot)
	if total.TotalTokens != llm.AddUsage(model.mainUsage, model.summaryUsage).TotalTokens || total.TotalTokens < 100_000 {
		t.Fatalf("persisted usage=%+v", total)
	}
	last := snapshot.Messages[len(snapshot.Messages)-1].Message.(llm.AssistantMessage)
	if !strings.Contains(last.ErrorMessage, "token budget") {
		t.Fatalf("last=%+v", last)
	}
	if _, err := sessionHistory(snapshot); err != nil {
		t.Fatalf("cannot resume history: %v", err)
	}
}
