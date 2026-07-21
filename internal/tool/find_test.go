package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestFindExecuteSupportsDoubleStarAndIgnoresGit(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "main.go", "package main")
	writeFixture(t, root, "internal/a.go", "package internal")
	writeFixture(t, root, "internal/a_test.go", "package internal")
	writeFixture(t, root, ".git/hidden.go", "package hidden")
	outside := writeFixture(t, t.TempDir(), "outside.go", "package outside")
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}
	find, err := tool.NewFind(workspace)
	if err != nil {
		t.Fatalf("NewFind() error = %v", err)
	}

	result, err := find.Execute(t.Context(), toolCall(t, "find", map[string]any{"pattern": "**/*.go"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "internal/a.go\ninternal/a_test.go\nmain.go"
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestFindExecuteUsesBasenamePatternsAndLimitsResults(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "a/one_test.go", "")
	writeFixture(t, root, "b/two_test.go", "")
	find, err := tool.NewFind(workspace)
	if err != nil {
		t.Fatalf("NewFind() error = %v", err)
	}

	result, err := find.Execute(t.Context(), toolCall(t, "find", map[string]any{
		"pattern": "*_test.go", "limit": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "result limit reached") {
		t.Fatalf("Execute() text = %q, want limit marker", got)
	}
}
