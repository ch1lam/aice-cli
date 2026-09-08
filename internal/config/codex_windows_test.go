package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCodexLockPendingDeletion(t *testing.T) {
	t.Parallel()
	paths := Paths{GlobalAuth: filepath.Join(t.TempDir(), "auth.json")}
	lock := CodexAuthPath(paths) + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(lock)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the removed directory pending until this handle is closed, as
	// another process observing the lock directory can do on Windows.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != windows.InvalidHandle {
			if err := windows.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	}()
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o700); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("create delete-pending directory = %v, want access denied", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	_, err = UpdateCodexCredentials(ctx, paths, func(CodexCredentials) (CodexCredentials, error) {
		t.Error("credential update ran without acquiring the lock")
		return CodexCredentials{}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("lock wait = %v, want deadline and access denied", err)
	}

	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = windows.InvalidHandle
	want := CodexCredentials{AccessToken: "access", RefreshToken: "refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}
	_, err = UpdateCodexCredentials(t.Context(), paths, func(CodexCredentials) (CodexCredentials, error) {
		return want, nil
	})
	if err != nil {
		t.Fatalf("lock after deletion completed: %v", err)
	}
	got, err := LoadCodexCredentials(paths)
	if err != nil || got.AccessToken != want.AccessToken {
		t.Fatalf("stored credential = %#v, %v", got, err)
	}
}
