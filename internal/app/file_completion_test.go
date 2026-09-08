package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestFileCompletionFuzzyPathsAndPermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	workspace, err := tool.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, gate, err := newExecutionGuard(dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/configuration.go", "src/中文 图.png", "README.md", ".env", "node_modules/hidden.js"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &interactiveSession{workspace: workspace, guardAdapter: gate}
	for query, want := range map[string]string{"cfg": "src/configuration.go", "sr/cfg": "src/configuration.go", "src/中": "src/中文 图.png", "read": "README.md"} {
		items, err := s.CompleteFiles(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 || items[0].Path != want {
			t.Fatalf("%q: %#v", query, items)
		}
	}
	for _, query := range []string{".env", "hidden", filepath.ToSlash(t.TempDir()) + "/"} {
		items, err := s.CompleteFiles(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("unexpected completion for %q: %#v", query, items)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	items, _ := s.CompleteFiles(ctx, "read")
	if len(items) != 0 {
		t.Fatal("cancelled search returned suggestions")
	}
}
