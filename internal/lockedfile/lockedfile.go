// Package lockedfile serializes read-modify-replace of small files across
// processes. Locks are OS file locks that the kernel releases when the holder
// exits, so a crash never leaves a stale lock. Replacement is atomic: readers
// see the old or the new complete document.
package lockedfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Win32 error codes are spelled here so retry behavior can be exercised on
// every test host without importing a Windows-only package.
const (
	accessDenied     syscall.Errno = 5  // ERROR_ACCESS_DENIED
	sharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
)

var errBusy = errors.New("lock is held by another process")

const retryInterval = 50 * time.Millisecond

// Lock acquires the exclusive lock guarding path, waiting while another
// process holds it until ctx ends. The lock lives in path + ".lock".
func Lock(ctx context.Context, path string) (unlock func() error, err error) {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	// Earlier AICE versions locked with an empty directory at this path.
	if info, err := os.Stat(lock); err == nil && info.IsDir() {
		_ = os.Remove(lock)
	}
	file, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lock, err)
	}
	err = retry(ctx, func() error { return tryLock(file) }, func(err error) bool { return errors.Is(err, errBusy) })
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("wait for %s: %w", lock, err)
	}
	return func() error { return unlockFile(file) }, nil
}

// Read reads path, retrying Windows sharing violations from a concurrent
// replacement. Missing files and other errors keep their meaning.
func Read(ctx context.Context, path string) ([]byte, error) {
	return read(ctx, path, os.ReadFile, runtime.GOOS == "windows")
}

func read(ctx context.Context, path string, readFile func(string) ([]byte, error), windows bool) ([]byte, error) {
	var data []byte
	err := retry(ctx, func() error {
		var err error
		data, err = readFile(path)
		return err
	}, func(err error) bool { return windows && errors.Is(err, sharingViolation) })
	return data, err
}

// Write atomically replaces path with data, creating its directory. Writers
// must hold Lock across their read-modify-write.
func Write(ctx context.Context, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return rename(ctx, file.Name(), path, os.Rename, runtime.GOOS == "windows")
}

// rename retries Windows sharing conflicts while the caller keeps the lock and
// temporary file. The destination is never removed first, so readers always
// see a complete file.
func rename(ctx context.Context, source, target string, renameFile func(string, string) error, windows bool) error {
	return retry(ctx, func() error { return renameFile(source, target) }, func(err error) bool {
		return windows && (errors.Is(err, accessDenied) || errors.Is(err, sharingViolation))
	})
}

// retry runs attempt until it succeeds, fails with a non-retryable error, or
// ctx ends. A context error is joined with the last retryable failure.
func retry(ctx context.Context, attempt func() error, retryable func(error) bool) error {
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, last)
		}
		err := attempt()
		if err == nil || !retryable(err) {
			return err
		}
		last = err
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
