package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestFileCompletionListsCurrentLevelBeyondEightResults(t *testing.T) {
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
	for _, folder := range []string{"assets", "internal", "internal/config", "internal/empty"} {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 12 {
		for _, folder := range []string{"", "assets", "internal"} {
			if err := os.WriteFile(filepath.Join(dir, folder, fmt.Sprintf("file%02d.go", i)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &interactiveSession{workspace: workspace, guardAdapter: gate}
	for _, query := range []string{"", "internal/"} {
		items, err := s.CompleteFiles(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 14 {
			t.Fatalf("%q: got %d candidates, want all 14 direct children", query, len(items))
		}
		if !items[0].Directory || !items[1].Directory {
			t.Fatalf("%q: directories not listed first: %#v", query, items)
		}
		for _, item := range items {
			if strings.Contains(strings.TrimSuffix(strings.TrimPrefix(item.Path, query), "/"), "/") {
				t.Fatalf("%q: descendant crowded out current level: %#v", query, item)
			}
		}
	}
	for query, want := range map[string]string{
		"itnl": "internal/", "internal/cfg": "internal/config/",
		"itnl/cfg": "internal/config/", "internal/f11": "internal/file11.go",
	} {
		items, err := s.CompleteFiles(t.Context(), query)
		if err != nil || len(items) == 0 || items[0].Path != want {
			t.Fatalf("%q: items = %#v, err = %v", query, items, err)
		}
	}
}
