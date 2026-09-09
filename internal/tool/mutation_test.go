package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/ch1lam/aice-cli/internal/llm"
)

type observedMutationFile struct {
	mutationFile
	after func(string) error
}

func (f *observedMutationFile) Write(p []byte) (int, error) {
	n, err := f.mutationFile.Write(p)
	return n, errors.Join(err, f.after("write"))
}
func (f *observedMutationFile) Sync() error {
	return errors.Join(f.mutationFile.Sync(), f.after("sync"))
}
func (f *observedMutationFile) Close() error {
	return errors.Join(f.mutationFile.Close(), f.after("close"))
}

func mutationTestTool(t *testing.T, name string) (*Workspace, func(context.Context) (llm.ToolResult, error), string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "target")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	var execute func(context.Context, llm.ToolCall) (llm.ToolResult, error)
	call := llm.ToolCall{ID: "mutation-1", Name: name}
	if name == "write" {
		tool, err := NewWrite(ws)
		if err != nil {
			t.Fatal(err)
		}
		execute = tool.Execute
		call.Arguments = []byte(`{"path":"target","content":"new"}`)
	} else {
		tool, err := NewEdit(ws)
		if err != nil {
			t.Fatal(err)
		}
		execute = tool.Execute
		call.Arguments = []byte(`{"path":"target","edits":[{"oldText":"old","newText":"new"}]}`)
	}
	return ws, func(ctx context.Context) (llm.ToolResult, error) { return execute(ctx, call) }, path
}

func assertMutationFiles(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if want == "absent" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target = %q, error = %v; want absent", data, err)
		}
	} else if err != nil || string(data) != want {
		t.Fatalf("target = %q, error = %v; want %q", data, err, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".aice-") {
			t.Errorf("temporary file remains: %s", entry.Name())
		}
	}
}

func TestMutationCancellationBoundaries(t *testing.T) {
	for _, name := range []string{"write", "edit"} {
		for _, boundary := range []string{"before", "write", "sync", "close", "rename"} {
			t.Run(name+"/"+boundary, func(t *testing.T) {
				t.Parallel()
				ws, execute, path := mutationTestTool(t, name)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				ops := ws.mutationOps
				ws.mutationOps.open = func(path string, flags int, mode os.FileMode) (mutationFile, error) {
					f, err := ops.open(path, flags, mode)
					if err != nil {
						return nil, err
					}
					return &observedMutationFile{f, func(stage string) error {
						if stage == boundary {
							cancel()
						}
						return nil
					}}, nil
				}
				renamed := false
				ws.mutationOps.rename = func(from, to string) error {
					renamed = true
					err := ops.rename(from, to)
					cancel()
					return err
				}
				if boundary == "before" {
					cancel()
				}
				result, err := execute(ctx)
				if boundary == "rename" {
					if err != nil || result.IsError || len(result.Content) == 0 {
						t.Fatalf("committed result = %+v, error = %v", result, err)
					}
					assertMutationFiles(t, path, "new")
				} else {
					if !errors.Is(err, context.Canceled) || renamed {
						t.Fatalf("error = %v, renamed = %v", err, renamed)
					}
					assertMutationFiles(t, path, "old")
				}
			})
		}
	}
}

func TestWriteCancellationLeavesNewTargetAbsent(t *testing.T) {
	ws, execute, path := mutationTestTool(t, "write")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	open := ws.mutationOps.open
	ws.mutationOps.open = func(path string, flags int, mode os.FileMode) (mutationFile, error) {
		f, err := open(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &observedMutationFile{f, func(stage string) error {
			if stage == "close" {
				cancel()
			}
			return nil
		}}, nil
	}
	if _, err := execute(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	assertMutationFiles(t, path, "absent")
}

func TestMutationErrorsPreserveTargetAndCleanup(t *testing.T) {
	for _, stage := range []string{"write", "sync", "close", "rename", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			ws, execute, path := mutationTestTool(t, "write")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("injected " + stage + " failure")
			ops := ws.mutationOps
			ws.mutationOps.open = func(path string, flags int, mode os.FileMode) (mutationFile, error) {
				f, err := ops.open(path, flags, mode)
				if err != nil {
					return nil, err
				}
				return &observedMutationFile{f, func(at string) error {
					if at == stage {
						cancel()
						return failure
					}
					if stage == "cleanup" && at == "close" {
						cancel()
					}
					return nil
				}}, nil
			}
			ws.mutationOps.rename = func(string, string) error { cancel(); return failure }
			if stage == "cleanup" {
				ws.mutationOps.remove = func(string) error { return failure }
			}
			_, err := execute(ctx)
			if !errors.Is(err, failure) {
				t.Fatalf("lost I/O error: %v", err)
			}
			if stage == "cleanup" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				paths, globErr := filepath.Glob(filepath.Join(ws.Path(), ".target.aice-*"))
				if globErr != nil || len(paths) != 1 {
					t.Fatalf("failed cleanup paths = %v, error = %v", paths, globErr)
				}
				if err := os.Remove(paths[0]); err != nil {
					t.Fatal(err)
				}
			}
			assertMutationFiles(t, path, "old")
		})
	}
}

func TestMutationCancellationWaitsForIOAndHoldsLock(t *testing.T) {
	for _, name := range []string{"write", "edit"} {
		for _, stage := range []string{"sync", "rename", "cleanup"} {
			t.Run(name+"/"+stage, func(t *testing.T) {
				ws, execute, path := mutationTestTool(t, name)
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					entered, release := make(chan struct{}), make(chan struct{})
					block := func() { close(entered); <-release }
					ops := ws.mutationOps
					ws.mutationOps.open = func(path string, flags int, mode os.FileMode) (mutationFile, error) {
						f, err := ops.open(path, flags, mode)
						if err != nil {
							return nil, err
						}
						return &observedMutationFile{f, func(at string) error {
							if stage == "sync" && at == "sync" {
								block()
							}
							if stage == "cleanup" && at == "close" {
								cancel()
							}
							return nil
						}}, nil
					}
					if stage == "rename" {
						ws.mutationOps.rename = func(from, to string) error { block(); return ops.rename(from, to) }
					}
					if stage == "cleanup" {
						ws.mutationOps.remove = func(path string) error { block(); return ops.remove(path) }
					}
					done := make(chan struct{})
					var executeErr error
					go func() { _, executeErr = execute(ctx); close(done) }()
					<-entered
					cancel()
					synctest.Wait()
					select {
					case <-done:
						t.Errorf("returned before I/O finished: %v", executeErr)
					default:
					}
					if ws.mutationMu.TryLock() {
						ws.mutationMu.Unlock()
						t.Error("mutation lock released during I/O")
					}
					close(release)
					<-done
					err := executeErr
					if stage == "rename" {
						if err != nil {
							t.Fatalf("committed write: %v", err)
						}
						assertMutationFiles(t, path, "new")
					} else {
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("error = %v", err)
						}
						assertMutationFiles(t, path, "old")
					}
					if !ws.mutationMu.TryLock() {
						t.Fatal("mutation lock not released after completion")
					}
					ws.mutationMu.Unlock()
				})
			})
		}
	}
}
