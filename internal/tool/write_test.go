package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestWriteExecuteCreatesFileWithoutApproval(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}

	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "created.txt", "content": "created",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "created.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got, want := string(data), "created"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func TestWriteExecuteCreatesAndAtomicallyReplacesFiles(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "nested/file.txt", "old")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("os.Chmod() error = %v", err)
	}
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}

	result, err := write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "nested/file.txt", "content": "new content",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "11 bytes") {
		t.Fatalf("Execute() text = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if got := string(data); got != "new content" {
		t.Fatalf("file content = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
}

func TestWriteExecuteRejectsWorkspaceEscapeBeforeMutation(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	write, err := tool.NewWrite(workspace)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}
	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "../outside.txt", "content": "no",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "outside.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file os.Stat() error = %v, want not exist", statErr)
	}

	outside := writeFixture(t, t.TempDir(), "outside.txt", "outside")
	if symlinkErr := os.Symlink(outside, filepath.Join(root, "escape")); symlinkErr != nil {
		t.Skipf("os.Symlink() error = %v", symlinkErr)
	}
	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "escape", "content": "no",
	}))
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Execute() error = %v, want symbolic-link error", err)
	}
	data, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("os.ReadFile() error = %v", readErr)
	}
	if got, want := string(data), "outside"; got != want {
		t.Fatalf("symlink target content = %q, want %q", got, want)
	}
}
