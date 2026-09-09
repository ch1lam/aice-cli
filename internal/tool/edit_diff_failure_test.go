package tool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestEditCommitFailureHasNoDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("rename failed")
	workspace.mutationOps.rename = func(string, string) error { return failure }
	edit, err := NewEdit(workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := edit.Execute(t.Context(), llm.ToolCall{ID: "edit-1", Name: "edit", Arguments: []byte(`{"path":"file","edits":[{"oldText":"old","newText":"new"}]}`)})
	if !errors.Is(err, failure) || result.Diff != (llm.ToolDiff{}) {
		t.Fatalf("result = %+v, %v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
}
