package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestRenameConfigFileRetry(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		windows      bool
		failures     []error
		cancelBefore bool
		cancelOnFail bool
		wantErr      error
		wantCause    error
		wantAttempts int
	}{
		{name: "immediate success", windows: true, wantAttempts: 1},
		{name: "Windows access denied then sharing violation", windows: true,
			failures: []error{syscall.Errno(5), syscall.Errno(32)}, wantAttempts: 3},
		{name: "Unix does not retry", failures: []error{syscall.Errno(5)},
			wantErr: syscall.Errno(5), wantAttempts: 1},
		{name: "other Windows error", windows: true, failures: []error{syscall.Errno(2)},
			wantErr: syscall.Errno(2), wantAttempts: 1},
		{name: "cancel before replacement", windows: true, cancelBefore: true,
			wantErr: context.Canceled},
		{name: "cancel during contention", windows: true, failures: []error{syscall.Errno(32)},
			cancelOnFail: true, wantErr: context.Canceled, wantCause: syscall.Errno(32), wantAttempts: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if tt.cancelBefore {
					cancel()
				}
				attempts := 0
				err := renameConfigFile(ctx, "temporary", "settings.json", func(source, target string) error {
					if source != "temporary" || target != "settings.json" {
						t.Fatalf("rename arguments = %q, %q", source, target)
					}
					attempts++
					if attempts <= len(tt.failures) {
						if tt.cancelOnFail {
							cancel()
						}
						return &os.LinkError{Op: "rename", Old: source, New: target, Err: tt.failures[attempts-1]}
					}
					return nil
				}, tt.windows)
				if !errors.Is(err, tt.wantErr) || (tt.wantCause != nil && !errors.Is(err, tt.wantCause)) {
					t.Fatalf("rename error = %v, want %v with cause %v", err, tt.wantErr, tt.wantCause)
				}
				if attempts != tt.wantAttempts {
					t.Fatalf("attempts = %d, want %d", attempts, tt.wantAttempts)
				}
			})
		})
	}
}

func TestRenameConfigFilePersistentConflict(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		code syscall.Errno
	}{
		{name: "access denied", code: 5},
		{name: "sharing violation", code: 32},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				attempts := 0
				start := time.Now()
				err := renameConfigFile(ctx, "temporary", "settings.json", func(source, target string) error {
					attempts++
					return &os.LinkError{Op: "rename", Old: source, New: target, Err: tt.code}
				}, true)
				if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, tt.code) {
					t.Fatalf("rename error = %v, want deadline and filesystem error", err)
				}
				if attempts < 2 || time.Since(start) != 5*time.Second {
					t.Fatalf("attempts = %d, elapsed = %v; want retries until deadline", attempts, time.Since(start))
				}
			})
		})
	}
}

func TestWriteJSONCanceledPreservesTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	const original = "{\"model\":\"old\"}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := writeJSON(ctx, path, map[string]string{"model": "new"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("write error = %v, want cancellation", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("original file changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatalf("temporary file not cleaned up: %v, %v", entries, err)
	}
}
