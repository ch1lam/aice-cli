package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestEditSymlinkCancellationCommit(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "before rename"
		if commit {
			name = "after rename"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ws, _, path := mutationTestTool(t, "edit")
			link := filepath.Join(ws.Path(), "alias")
			if err := os.Symlink("target", link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			editor, err := NewEdit(ws)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ops := ws.mutationOps
			ws.mutationOps.open = func(path string, flags int, mode os.FileMode) (mutationFile, error) {
				f, err := ops.open(path, flags, mode)
				if err != nil {
					return nil, err
				}
				return &observedMutationFile{f, func(stage string) error {
					if !commit && stage == "close" {
						cancel()
					}
					return nil
				}}, nil
			}
			ws.mutationOps.rename = func(from, to string) error {
				if to != filepath.Join(ws.PhysicalPath(), "target") {
					t.Errorf("rename target = %q", to)
				}
				err := ops.rename(from, to)
				cancel()
				return err
			}
			result, err := editor.Execute(ctx, llm.ToolCall{ID: "edit", Name: "edit", Arguments: []byte(`{"path":"alias","edits":[{"oldText":"old","newText":"new"}]}`)})
			want := "old"
			if commit {
				want = "new"
				if err != nil || result.IsError {
					t.Fatalf("committed edit = %+v, %v", result, err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
			assertMutationFiles(t, path, want)
			if got, err := os.Readlink(link); err != nil || got != "target" {
				t.Fatalf("link = %q, %v", got, err)
			}
		})
	}
}
