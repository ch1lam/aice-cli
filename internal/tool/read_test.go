package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestReadExecuteReturnsSelectedLines(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	absolutePath := writeFixture(t, root, "notes.txt", "one\ntwo\nthree\nfour\n")
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": absolutePath, "offset": 2, "limit": 2,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "two\nthree\n\n[showing lines 2-3; more lines available]"
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestReadExecuteUsesHostPaths(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	workspace := newWorkspaceAt(t, root)
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	outside := writeFixture(t, parent, "secret.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "relative traversal", path: "../secret.txt"},
		{name: "absolute outside path", path: outside},
		{name: "symlink", path: "escape"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := read.Execute(
				t.Context(),
				toolCall(t, "read", map[string]any{"path": test.path}),
			)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := resultText(t, result), "secret"; got != want {
				t.Fatalf("Execute() text = %q, want %q", got, want)
			}
		})
	}
}

func TestReadExecuteResolvesParentAfterWorkingDirectorySymlink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	physicalParent := filepath.Join(base, "physical")
	physicalWork := filepath.Join(physicalParent, "work")
	if err := os.MkdirAll(physicalWork, 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	writeFixture(t, physicalParent, "sibling.txt", "physical sibling")
	linkedWork := filepath.Join(base, "linked-work")
	if err := os.Symlink(physicalWork, linkedWork); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}

	workspace := newWorkspaceAt(t, linkedWork)
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": "../sibling.txt",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "physical sibling"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestReadExecuteRejectsBinaryAndUnknownArguments(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "binary.dat", "a\x00b")
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	_, err = read.Execute(t.Context(), toolCall(t, "read", map[string]any{"path": "binary.dat"}))
	if err == nil || !strings.Contains(err.Error(), "binary file") {
		t.Fatalf("Execute() error = %v, want binary-file error", err)
	}
	_, err = read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": "binary.dat", "unexpected": true,
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Execute() error = %v, want unknown-field error", err)
	}
}

func TestReadExecuteBoundsOutput(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "large.txt", strings.Repeat("a", 60*1024))
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{"path": "large.txt"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if len(text) > 50*1024 || !strings.HasSuffix(text, "[output truncated]") {
		t.Fatalf("Execute() output length = %d, suffix = %q", len(text), text[max(0, len(text)-32):])
	}
}
