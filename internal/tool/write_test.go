package tool_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestWriteExecuteDefaultsToDeny(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	write, err := tool.NewWrite(workspace, nil)
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}

	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "denied.txt", "content": "no",
	}))
	if !errors.Is(err, tool.ErrApprovalRequired) {
		t.Fatalf("Execute() error = %v, want ErrApprovalRequired", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "denied.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("os.Stat() error = %v, want not exist", statErr)
	}
}

func TestWriteExecuteCreatesAndAtomicallyReplacesFiles(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	path := writeFixture(t, root, "nested/file.txt", "old")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("os.Chmod() error = %v", err)
	}
	write, err := tool.NewWrite(workspace, allowAll())
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

func TestWriteExecuteRejectsWorkspaceEscapeBeforeApproval(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	approved := false
	write, err := tool.NewWrite(workspace, tool.ApproverFunc(func(_ context.Context, _ tool.ApprovalRequest) error {
		approved = true
		return nil
	}))
	if err != nil {
		t.Fatalf("NewWrite() error = %v", err)
	}
	_, err = write.Execute(t.Context(), toolCall(t, "write", map[string]any{
		"path": "../outside.txt", "content": "no",
	}))
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if approved {
		t.Fatal("approver called for an invalid path")
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
	if approved {
		t.Fatal("approver called for a symbolic link")
	}
}
