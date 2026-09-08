package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func completionTestModel() model {
	m := newModel(make(chan runRequest), make(chan struct{}))
	m.completeFiles = func(generation uint64, query string) (tea.Cmd, context.CancelFunc) {
		return func() tea.Msg {
			return fileCompletionResult{generation: generation, items: []interaction.FileCompletion{{Path: query + " file.png"}}}
		}, func() {}
	}
	return m
}

func TestFileCompletionIgnoresStaleResultsAndQuotesSelection(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("look @im")
	old := m.requestFileCompletion()
	m.input.SetValue("look @new")
	latest := m.requestFileCompletion()
	m = updateModel(t, m, old())
	if len(m.fileCompletion.items) != 0 {
		t.Fatal("stale result applied")
	}
	m = updateModel(t, m, latest())
	if !m.fileCompletionVisible() {
		t.Fatal("suggestions missing")
	}
	m, _, handled := m.handleFileCompletionKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if !handled || m.input.Value() != `look @"new file.png"` || m.fileCompletionVisible() {
		t.Fatalf("selection = %q", m.input.Value())
	}
	refs := interaction.FileReferences(m.input.Value())
	if len(refs) != 1 || refs[0] != "new file.png" {
		t.Fatal("selected path did not roundtrip")
	}
}

func TestFileReferencesSkipOpaquePasteAndPreserveDraftOnAsyncFailure(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("inspect @main.go ")
	m.insertPastePlaceholder(strings.Repeat("literal @missing\n", 10))
	m, command, _ := m.submit() // Inspect the frozen request without starting a controller.
	_ = command
	if m.submittedInput == nil || len(m.submittedInput.Files) != 1 || m.submittedInput.Files[0] != "main.go" {
		t.Fatal("literal paste became a file reference")
	}
	// Do not start the controller: preflight failure is rendered from a terminal update.
	m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{done: true, err: context.Canceled}}})
	if !strings.Contains(m.input.Value(), "@main.go") || len(m.pastes) != 1 {
		t.Fatal("cancelled input lost draft")
	}
}

func TestCancelledApprovalRestoresComposerAndDoesNotDismissNewRequest(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	done := make(chan struct{})
	reply := make(chan interaction.GuardReply, 1)
	req := &interaction.GuardRequest{Done: done, Reply: reply, Path: "outside.txt"}
	updated, command := m.Update(guardRequestMsg{req: req})
	m = updated.(model)
	if command == nil || m.guardPending == nil {
		t.Fatal("approval not watching cancellation")
	}
	close(done)
	m = updateModel(t, m, command())
	if m.guardPending != nil {
		t.Fatal("cancelled approval remained visible")
	}
	newer := &interaction.GuardRequest{Reply: make(chan interaction.GuardReply, 1), Path: "new.txt"}
	m = updateModel(t, m, guardRequestMsg{req: newer})
	m = updateModel(t, m, guardExpiredMsg{reply: reply})
	if m.guardPending != newer {
		t.Fatal("old cancellation dismissed newer approval")
	}
}
