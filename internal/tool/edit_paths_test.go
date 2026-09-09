package tool_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestEditExecutePreservesSymlinks(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"relative", "absolute", "chain", "parent", "parent traversal"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			workspace, root := newWorkspace(t)
			target := writeFixture(t, root, "real/file.txt", "old")
			if err := os.Chmod(target, 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "alias")
			destination, input := "real/file.txt", "alias"
			switch kind {
			case "absolute":
				destination = target
			case "chain":
				if err := os.Symlink("real/file.txt", filepath.Join(root, "middle")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				destination = "middle"
			case "parent":
				destination, input = "real", "alias/file.txt"
			case "parent traversal":
				if err := os.Mkdir(filepath.Join(root, "real", "child"), 0750); err != nil {
					t.Fatal(err)
				}
				destination, input = "real/child", "alias/../file.txt"
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			// Retain the original file without holding an open handle across rename.
			// Windows os.SameFile can load identity from a Stat path lazily, after
			// that path already refers to the replacement.
			original := filepath.Join(root, "original.txt")
			if err := os.Link(target, original); err != nil {
				t.Fatal(err)
			}
			editor, err := tool.NewEdit(workspace)
			if err != nil {
				t.Fatal(err)
			}
			_, err = editor.Execute(t.Context(), toolCall(t, "edit", map[string]any{"path": input, "edits": []map[string]string{{"oldText": "old", "newText": "new"}}}))
			if err != nil {
				t.Fatal(err)
			}
			gotLink, err := os.Readlink(link)
			if err != nil || gotLink != filepath.FromSlash(destination) {
				t.Fatalf("link = %q, %v", gotLink, err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "new" {
				t.Fatalf("target = %q, %v", data, err)
			}
			after, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			oldData, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			if string(oldData) != "old" {
				t.Fatalf("target was modified in place instead of replaced: original = %q", oldData)
			}
			if runtime.GOOS != "windows" && after.Mode().Perm() != 0600 {
				t.Fatalf("mode = %o", after.Mode().Perm())
			}
			if kind == "chain" {
				if got, err := os.Readlink(filepath.Join(root, "middle")); err != nil || got != filepath.FromSlash("real/file.txt") {
					t.Fatalf("middle = %q, %v", got, err)
				}
			}
		})
	}
}

func TestEditExecuteRejectsUnresolvedSymlinks(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"dangling", "dangling parent", "cycle", "cycle parent", "dangling chain"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			workspace, root := newWorkspace(t)
			destination, input := "missing/file.txt", "alias"
			if strings.Contains(kind, "cycle") {
				destination = "alias"
			}
			if strings.Contains(kind, "parent") {
				input = "alias/new/file.txt"
			}
			if kind == "dangling chain" {
				if err := os.Symlink(destination, filepath.Join(root, "middle")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				destination = "middle"
			}
			link := filepath.Join(root, "alias")
			if err := os.Symlink(destination, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			before, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			editor, err := tool.NewEdit(workspace)
			if err != nil {
				t.Fatal(err)
			}
			_, err = editor.Execute(t.Context(), toolCall(t, "edit", map[string]any{"path": input, "edits": []map[string]string{{"oldText": "old", "newText": "new"}}}))
			if err == nil {
				t.Fatal("unresolved link accepted")
			}
			after, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != len(after) {
				t.Fatalf("unexpected side effects: %v", after)
			}
			if got, err := os.Readlink(link); err != nil || got != filepath.FromSlash(destination) {
				t.Fatalf("link = %q, %v", got, err)
			}
		})
	}
}

func TestEditPathsRequireExistingRegularTarget(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"missing", "missing/parents/file", "missing/../file", "real", "alias", "alias/missing", "file/child", "file/"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			ws, root := newWorkspace(t)
			writeFixture(t, root, "real/keep", "old")
			writeFixture(t, root, "file", "old")
			if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			editor, err := tool.NewEdit(ws)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := editor.ResolvePath(input); err == nil {
				t.Fatal("invalid edit target resolved")
			}
			if _, err := editor.Execute(t.Context(), toolCall(t, "edit", map[string]any{"path": input, "edits": []map[string]string{{"oldText": "old", "newText": "new"}}})); err == nil {
				t.Fatal("invalid edit target accepted")
			}
			for _, name := range []string{"file", "real/keep"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(data) != "old" {
					t.Fatalf("file changed: %q, %v", data, err)
				}
			}
			for _, name := range []string{"missing", "real/missing"} {
				if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatalf("missing path created: %s, %v", name, err)
				}
			}
		})
	}
}
