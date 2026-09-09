package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestGrepPathNormalization(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "a b.txt", "needle normalized\n")
	writeFixture(t, root, "a\u3000b.txt", "wrong literal\n")
	writeFixture(t, root, "@literal.txt", "needle literal\n")
	writeFixture(t, root, "~/literal.txt", "needle tilde\n")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(home, filepath.Join(root, "a b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"@a b.txt", "a\u3000b.txt", "@a\u3000b.txt", "~/" + filepath.ToSlash(rel), "@~/" + filepath.ToSlash(rel), "@@literal.txt", "./@literal.txt", "./~/literal.txt"} {
		t.Run(input, func(t *testing.T) {
			result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{"path": input, "pattern": "needle"}))
			if err != nil {
				t.Fatal(err)
			}
			if output := resultText(t, result); !strings.Contains(output, ":1: needle") {
				t.Fatalf("normalized search failed: %q", output)
			}
		})
	}
	writeFixture(t, root, "it’s.txt", "needle fallback\n")
	if _, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{"path": "it's.txt", "pattern": "needle"})); err == nil {
		t.Fatal("grep unexpectedly selected a read-only filename variant")
	}
}

func TestGrepResolvesSymlinkBeforeParentTraversal(t *testing.T) {
	t.Parallel()
	requireRipgrep(t)
	workspace, root := newWorkspace(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, outside, "target.txt", "needle outside\n")
	writeFixture(t, root, "target.txt", "wrong lexical target\n")
	if err := os.Symlink(filepath.Join(outside, "child"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	grep, err := tool.NewGrep(workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := grep.Execute(t.Context(), toolCall(t, "grep", map[string]any{"path": "@alias/../target.txt", "pattern": "needle"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(t, result); got != "target.txt:1: needle outside" {
		t.Fatalf("wrong target: %q", got)
	}
}
