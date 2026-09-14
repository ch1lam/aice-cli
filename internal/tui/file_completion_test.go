package tui

import (
	"context"
	"fmt"
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

func TestFileCompletionConfirmDoesNotSubmit(t *testing.T) {
	t.Parallel()
	for _, code := range []rune{tea.KeyTab, tea.KeyEnter} {
		for _, item := range []interaction.FileCompletion{
			{Path: "internal/", Directory: true}, {Path: "images/中文 图.png"},
		} {
			t.Run(fmt.Sprintf("%d/%s", code, item.Path), func(t *testing.T) {
				m := completionTestModel()
				m.input.SetValue("look @i")
				m.requestFileCompletion()
				m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{item}})
				m = updateModel(t, m, tea.KeyPressMsg{Code: code})
				if want := "look @" + item.Path + " "; m.input.Value() != want {
					t.Fatalf("draft = %q, want %q", m.input.Value(), want)
				}
				if m.running || m.submittedInput != nil || m.fileCompletionVisible() {
					t.Fatal("confirmation sent the draft or kept completion open")
				}
			})
		}
	}
}

func TestFileCompletionRightContinuesMatchingAndPreservesTail(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("看 @i 后续 @README.md")
	for range []rune(" 后续 @README.md") {
		m.input, _ = m.input.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	m.requestFileCompletion()
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{
		{Path: "internal/", Directory: true}, {Path: "中文 目录/", Directory: true},
	}})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	updated, command := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(model)
	if want := `看 @中文 目录/ 后续 @README.md`; m.input.Value() != want || command == nil || m.running {
		t.Fatalf("drilled draft = %q, command missing = %v", m.input.Value(), command == nil)
	}
	ref, ok := m.fileReferenceAtCursor()
	if !ok || ref.Path != "中文 目录/" || !m.fileCompletion.pending {
		t.Fatalf("drill lost completion context: %#v", ref)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Text: "cfg"})
	if m.fileCompletion.ref.Path != "中文 目录/cfg" {
		t.Fatalf("nested query = %q", m.fileCompletion.ref.Path)
	}
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{{Path: "中文 目录/config.go"}}})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if ref, ok := m.fileReferenceAtCursor(); !ok || ref.Path != "中文 目录/config.go" {
		t.Fatalf("Right on file ended matching: %#v", ref)
	}
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{{Path: "中文 目录/config.go"}}})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if want := `看 @中文 目录/config.go 后续 @README.md`; m.input.Value() != want || m.running || m.fileCompletionVisible() {
		t.Fatalf("confirmed draft = %q", m.input.Value())
	}
}

func TestFileCompletionRightIsUnquotedAndEditable(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"internal/", "中文 目录/config.go", `folder/"quoted" file.go`} {
		t.Run(path, func(t *testing.T) {
			m := completionTestModel()
			m.input.SetValue("look @i")
			m.requestFileCompletion()
			for range 2 {
				m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{{Path: path}}})
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
			}
			if m.input.Value() != "look @"+path || len(m.input.files) != 1 || !m.input.files[0].editing {
				t.Fatalf("expanded path = %q, spans = %#v", m.input.Value(), m.input.files)
			}
			if len(m.input.fileSpansInRow(0)) != 0 {
				t.Fatal("unconfirmed expansion became atomic")
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
			runes := []rune(path)
			want := string(runes[:len(runes)-1])
			if ref, ok := m.fileReferenceAtCursor(); !ok || ref.Path != want || m.input.Value() != "look @"+want {
				t.Fatalf("backspace lost editable query: %q / %#v", m.input.Value(), ref)
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
			if _, ok := m.fileReferenceAtCursor(); ok {
				t.Fatal("typed separator did not finish the query")
			}
			files := m.composerFiles()
			if len(files) != 1 || files[0] != want {
				t.Fatalf("space in expanded path was split during submission: %#v", files)
			}
		})
	}
}

func TestFileCompletionRightReplacesWholeEditedPathInMiddle(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("@i tail @README.md")
	m.input.SetCursorColumn(2)
	m.requestFileCompletion()
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{{Path: "my folder/config.go"}}})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m.input.SetCursorColumn(len("@my folder/cfg"))
	m.requestFileCompletion()
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: []interaction.FileCompletion{{Path: "my folder/config_test.go"}}})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != "@my folder/config_test.go tail @README.md" || len(m.input.files) != 1 || m.input.files[0].editing {
		t.Fatalf("confirming mid-path damaged the draft: %q", m.input.Value())
	}
}

func TestFileCompletionPendingKeysNeverSubmitOrUseStaleResults(t *testing.T) {
	t.Parallel()
	for _, retained := range []bool{false, true} {
		for _, code := range []rune{tea.KeyTab, tea.KeyEnter, tea.KeyRight} {
			m := completionTestModel()
			m.input.SetValue("@i")
			m.requestFileCompletion()
			if retained {
				m.fileCompletion.items = []interaction.FileCompletion{{Path: "old.go"}}
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: code})
			if m.input.Value() != "@i" || m.running || m.submittedInput != nil {
				t.Fatalf("pending key %d (retained %v) changed/submitted draft: %q", code, retained, m.input.Value())
			}
		}
	}
}

func TestFileCompletionScrollsBeyondFirstPage(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m.input.SetValue("@")
	m.requestFileCompletion()
	items := make([]interaction.FileCompletion, 15)
	for i := range items {
		items[i].Path = fmt.Sprintf("file%02d.go", i)
	}
	m = updateModel(t, m, fileCompletionResult{generation: m.fileCompletion.generation, items: items})
	for range 12 {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := m.fileCompletionView(m.width)
	if !strings.Contains(view, "file12.go") || !strings.Contains(view, "13/15") || strings.Contains(view, "file00.go") {
		t.Fatalf("scrolled menu = %q", view)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Value() != `@file12.go ` || m.running {
		t.Fatalf("scrolled selection = %q", m.input.Value())
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.submittedInput == nil || len(m.submittedInput.Files) != 1 || m.submittedInput.Files[0] != "file12.go" {
		t.Fatal("second Enter did not submit confirmed reference")
	}
}

func TestFileCompletionModifiedEnterStillInsertsNewline(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m.input.SetValue("@i")
	m = updateModel(t, m, m.requestFileCompletion()())
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if m.input.Value() != "@i\n" || m.running {
		t.Fatalf("Shift+Enter = %q", m.input.Value())
	}
}

func TestFileCompletionIgnoresStaleResultsAndAttachesSelection(t *testing.T) {
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
	if !handled || m.input.Value() != `look @new file.png ` || m.fileCompletionVisible() {
		t.Fatalf("selection = %q", m.input.Value())
	}
	refs := m.composerFiles()
	if len(refs) != 1 || refs[0] != "new file.png" {
		t.Fatal("selected path did not roundtrip")
	}
}

func TestFileCompletionKeepsLayoutWhileTyping(t *testing.T) {
	t.Parallel()
	m := completionTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.input.SetValue("look @i")
	m = updateModel(t, m, m.requestFileCompletion()())
	menuHeight := strings.Count(m.fileCompletionView(m.width), "\n")
	transcript := m.viewport.View()
	for _, letter := range "mag" {
		m = updateModel(t, m, tea.KeyPressMsg{Code: letter, Text: string(letter)})
		if !m.fileCompletionVisible() || strings.Count(m.fileCompletionView(m.width), "\n") != menuHeight {
			t.Fatal("completion menu collapsed while waiting for results")
		}
		if m.viewport.View() != transcript {
			t.Fatal("typing a query moved the transcript")
		}
		before := m.input.Value()
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.input.Value() != before {
			t.Fatal("Tab attached an outdated candidate")
		}
	}
	m = updateModel(t, m, fileCompletionResult{
		generation: m.fileCompletion.generation,
		items:      []interaction.FileCompletion{{Path: "image.png"}},
	})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != `look @image.png ` {
		t.Fatalf("latest candidate was not attached: %q", m.input.Value())
	}
}

func TestFileCompletionPendingResultLifecycle(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"empty", "error", "escape", "leave token"} {
		t.Run(action, func(t *testing.T) {
			m := completionTestModel()
			m.input.SetValue("@i")
			m = updateModel(t, m, m.requestFileCompletion()())
			m = updateModel(t, m, tea.KeyPressMsg{Code: 'm', Text: "m"})
			result := fileCompletionResult{generation: m.fileCompletion.generation}
			// Even failed older searches must not close the retained menu.
			m = updateModel(t, m, fileCompletionResult{generation: result.generation - 1, err: context.Canceled})
			if !m.fileCompletionVisible() {
				t.Fatal("stale failure closed the menu")
			}
			switch action {
			case "error":
				result.err = context.DeadlineExceeded
				result.items = []interaction.FileCompletion{{Path: "ignored.png"}}
			case "escape":
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
				result.items = []interaction.FileCompletion{{Path: "image.png"}}
			case "leave token":
				m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
				result.items = []interaction.FileCompletion{{Path: "image.png"}}
			}
			m = updateModel(t, m, result)
			if m.fileCompletionVisible() {
				t.Fatal("completion menu remained visible or reopened")
			}
		})
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
