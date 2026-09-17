//go:build unix

package session

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCloseReleasesWriterWithInheritedDescriptor(t *testing.T) {
	for _, reopen := range []bool{false, true} {
		name := "create"
		if reopen {
			name = "open"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.jsonl")
			store, err := Create(t.Context(), path, Metadata{
				ID: "session-1", CreatedAt: 100, WorkingDirectory: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if reopen {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				store, err = Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
			}
			file := store.file
			if store.lockFile != nil {
				file = store.lockFile
			}
			// A child between fork and exec retains the same open file description,
			// even with close-on-exec set. Dup reproduces that lifetime deterministically.
			fd, err := syscall.Dup(int(file.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			inherited := os.NewFile(uintptr(fd), path)
			defer inherited.Close()
			if other, err := Open(t.Context(), path); !errors.Is(err, ErrBusy) {
				if other != nil {
					other.Close()
				}
				t.Fatalf("active writer did not exclude another writer: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			other, err := Open(t.Context(), path)
			if err != nil {
				t.Fatalf("closed writer retained lock through inherited descriptor: %v", err)
			}
			defer other.Close()
		})
	}
}
