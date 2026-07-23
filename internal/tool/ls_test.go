package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestLSExecuteListsSortedEntries(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "b.txt", "b")
	if err := os.Mkdir(filepath.Join(root, "a"), 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	if err := os.Symlink("b.txt", filepath.Join(root, "link")); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatalf("NewLS() error = %v", err)
	}

	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "a/\nb.txt\nlink@"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestLSExecuteEnforcesLimit(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "a.txt", "a")
	writeFixture(t, root, "b.txt", "b")
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatalf("NewLS() error = %v", err)
	}

	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{"limit": 1}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "entry limit reached") {
		t.Fatalf("Execute() text = %q, want limit marker", got)
	}
}

func TestLSExecuteListsParentDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixture(t, parent, "outside.txt", "outside")
	workspace := newWorkspaceAt(t, root)
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatalf("NewLS() error = %v", err)
	}

	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{"path": ".."}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "outside.txt\nwork/"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}
