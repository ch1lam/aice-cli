//go:build windows

package trust

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	maxLockAttempts = 10
	lockRetryDelay  = 20 * time.Millisecond
	staleLockAge    = 30 * time.Second
)

// acquireLock takes a lock on the lock file by creating it exclusively. The
// returned function removes the lock file. A lock older than staleLockAge is
// considered abandoned after a crashed process and is reclaimed.
func acquireLock(path string) (func() error, error) {
	for attempt := 1; attempt <= maxLockAttempts; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			file.Close()
			return func() error {
				err := os.Remove(path)
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("trust: acquire lock %s: %w", path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil &&
			time.Since(info.ModTime()) > staleLockAge {
			if removeErr := os.Remove(path); removeErr == nil {
				continue
			}
		}
		if attempt == maxLockAttempts {
			return nil, fmt.Errorf(
				"trust: acquire lock %s: still locked after %d attempts",
				path,
				maxLockAttempts,
			)
		}
		time.Sleep(lockRetryDelay)
	}
	return nil, fmt.Errorf("trust: acquire lock %s: too many attempts", path)
}
