package tool_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestGrepExecuteSearchesWithPiCompatibleArguments(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "a.go", "before\nNeedle value\nafter\n")
	writeFixture(t, root, "a.txt", "needle ignored\n")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern": "needle", "glob": "*.go", "ignoreCase": true, "literal": true, "context": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "a.go-1-before\na.go:2:Needle value\na.go-3-after"
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteSearchesParentDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixture(t, parent, "outside.go", "needle\n")
	workspace := newWorkspaceAt(t, root)
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{
		"pattern": "needle",
		"path":    "..",
		"glob":    "*.go",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "../outside.go:1:needle"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestGrepExecuteSkipsBinaryAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "binary.dat", "needle\x00data")
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatalf("NewGrep() error = %v", err)
	}

	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{"pattern": "needle"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "skipped 1") {
		t.Fatalf("Execute() text = %q, want skipped marker", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = grep.Execute(ctx, toolCall(t, "grep", map[string]any{"pattern": "needle"}))
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Execute() error = %v, want cancellation", err)
	}
}
