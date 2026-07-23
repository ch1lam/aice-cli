package tool_test

import (
	"os"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestEditExecuteAppliesDisjointEditsAgainstOriginal(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "file.txt", "alpha beta gamma\n")
	edit, err := tool.NewEdit(workspace)
	if err != nil {
		t.Fatalf("NewEdit() error = %v", err)
	}

	result, err := edit.Execute(t.Context(), toolCall(t, "edit", map[string]any{
		"path": "file.txt",
		"edits": []map[string]string{
			{"oldText": "alpha", "newText": "one"},
			{"oldText": "gamma", "newText": "three"},
		},
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "2 block(s)") {
		t.Fatalf("Execute() text = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(data), "one beta three\n"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func TestEditExecuteRejectsAmbiguousEditBeforeMutation(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "file.txt", "same same\n")
	edit, err := tool.NewEdit(workspace)
	if err != nil {
		t.Fatalf("NewEdit() error = %v", err)
	}
	_, err = edit.Execute(t.Context(), toolCall(t, "edit", map[string]any{
		"path": "file.txt", "edits": []map[string]string{{"oldText": "same", "newText": "new"}},
	}))
	if err == nil || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("Execute() error = %v, want ambiguity error", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("os.ReadFile() error = %v", readErr)
	}
	if got := string(data); got != "same same\n" {
		t.Fatalf("file content = %q after invalid edit", got)
	}
}

func TestEditExecutePreservesBOMAndCRLF(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "windows.txt", "\ufeffone\r\ntwo\r\n")
	edit, err := tool.NewEdit(workspace)
	if err != nil {
		t.Fatalf("NewEdit() error = %v", err)
	}
	_, err = edit.Execute(t.Context(), toolCall(t, "edit", map[string]any{
		"path": "windows.txt", "edits": []map[string]string{{"oldText": "one\ntwo", "newText": "first\nsecond"}},
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(data), "\ufefffirst\r\nsecond\r\n"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}
