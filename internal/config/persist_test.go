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

func TestReadConfigFileRetry(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		windows      bool
		failure      error
		persistent   bool
		cancelBefore bool
		cancelOnFail bool
		wantErr      error
		wantAttempts int
	}{
		{name: "immediate success", windows: true, wantAttempts: 1},
		{name: "Windows sharing violation", windows: true, failure: syscall.Errno(32), wantAttempts: 2},
		{name: "Unix does not retry", failure: syscall.Errno(32), wantErr: syscall.Errno(32), wantAttempts: 1},
		{name: "access denied is not sharing contention", windows: true, failure: syscall.Errno(5), wantErr: syscall.Errno(5), wantAttempts: 1},
		{name: "missing file", windows: true, failure: os.ErrNotExist, wantErr: os.ErrNotExist, wantAttempts: 1},
		{name: "other read error", windows: true, failure: syscall.EIO, wantErr: syscall.EIO, wantAttempts: 1},
		{name: "cancel before read", windows: true, cancelBefore: true, wantErr: context.Canceled},
		{name: "cancel during contention", windows: true, failure: syscall.Errno(32), cancelOnFail: true, wantErr: context.Canceled, wantAttempts: 1},
		{name: "persistent contention", windows: true, failure: syscall.Errno(32), persistent: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if tt.cancelBefore {
					cancel()
				}
				const document = `{"openai_api_key":"fixture"}`
				attempts := 0
				start := time.Now()
				data, err := readConfigFile(ctx, "auth.json", func(path string) ([]byte, error) {
					if path != "auth.json" {
						t.Fatalf("read path = %q", path)
					}
					attempts++
					if tt.failure != nil && (attempts == 1 || tt.persistent) {
						if tt.cancelOnFail {
							cancel()
						}
						return nil, &os.PathError{Op: "open", Path: path, Err: tt.failure}
					}
					return []byte(document), nil
				}, tt.windows)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("read error = %v, want %v", err, tt.wantErr)
				}
				if (tt.cancelOnFail || tt.persistent) && !errors.Is(err, tt.failure) {
					t.Fatalf("filesystem cause lost: %v", err)
				}
				if tt.persistent {
					if attempts < 2 || time.Since(start) != 5*time.Second {
						t.Fatalf("attempts = %d, elapsed = %v; want retries until deadline", attempts, time.Since(start))
					}
				} else if attempts != tt.wantAttempts {
					t.Fatalf("attempts = %d, want %d", attempts, tt.wantAttempts)
				}
				if err == nil && string(data) != document {
					t.Fatalf("read bytes = %q, want complete document", data)
				}
			})
		})
	}
}

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
