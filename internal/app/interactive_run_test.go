package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestInteractiveSessionEnsureSessionStoreCreatesOnDemand(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	workspace, err := tool.NewWorkspace(workspacePath)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	runner := &interactiveSession{workspace: workspace}
	defer func() {
		if runner.store != nil {
			if err := runner.store.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		}
	}()

	if err := runner.ensureSessionStore(); err != nil {
		t.Fatalf("ensureSessionStore() error = %v", err)
	}
	path := runner.store.Path()
	if want := filepath.Join(workspacePath, ".aice", "sessions"); !strings.HasPrefix(path, want) {
		t.Errorf("store path = %q, want it under %q", path, want)
	}
	snapshot, err := runner.store.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Turns) != 0 {
		t.Fatalf("turns = %d, want a fresh session", len(snapshot.Turns))
	}
	if snapshot.Header.WorkingDirectory != workspace.Path() {
		t.Errorf(
			"working directory = %q, want %q",
			snapshot.Header.WorkingDirectory,
			workspace.Path(),
		)
	}
	if err := runner.ensureSessionStore(); err != nil {
		t.Fatalf("second ensureSessionStore() error = %v", err)
	}
	if got := runner.store.Path(); got != path {
		t.Fatalf("store path after second ensure = %q, want %q", got, path)
	}
	matches, err := filepath.Glob(filepath.Join(workspacePath, ".aice", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("session files = %#v, want exactly one", matches)
	}
}

func TestInteractiveSessionEnsureSessionStoreRequiresWorkspace(t *testing.T) {
	t.Parallel()

	runner := &interactiveSession{}
	if err := runner.ensureSessionStore(); err == nil {
		t.Fatal("ensureSessionStore() error = nil, want workspace error")
	}
	if runner.store != nil {
		t.Fatal("store created without a workspace")
	}
}

func TestInteractiveSessionNewRunCreatesSessionLazily(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	workspace, err := tool.NewWorkspace(workspacePath)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	model := &recordingModel{response: "answer"}
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatalf("agent.NewLoop() error = %v", err)
	}
	runner := &interactiveSession{loop: loop, workspace: workspace}
	defer func() {
		if runner.store != nil {
			if err := runner.store.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		}
	}()
	if runner.store != nil {
		t.Fatal("store exists before the first prompt")
	}

	active, err := runner.NewRun(interaction.RunInput{Prompt: "hello"}, nil)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	if active == nil {
		t.Fatal("NewRun() run = nil, want an active run")
	}
	if runner.store == nil {
		t.Fatal("store missing after the first prompt was accepted")
	}
}
