//go:build windows

package trust

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	maxLockAttempts = 10
	lockRetryDelay  = 20 * time.Millisecond
)

// acquireLock takes an exclusive byte-range lock on the lock file. The
// returned function releases the lock and closes the file. Windows releases
// byte-range locks automatically when the owning process exits, so a crashed
// process cannot leave a stale lock behind.
func acquireLock(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trust: open lock %s: %w", path, err)
	}
	var ol windows.Overlapped
	for attempt := 1; attempt <= maxLockAttempts; attempt++ {
		err = windows.LockFileEx(
			windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, &ol,
		)
		if err == nil {
			return func() error {
				return errors.Join(
					windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &ol),
					file.Close(),
				)
			}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			file.Close()
			return nil, fmt.Errorf("trust: acquire lock %s: %w", path, err)
		}
		if attempt == maxLockAttempts {
			file.Close()
			return nil, fmt.Errorf(
				"trust: acquire lock %s: still locked after %d attempts",
				path,
				maxLockAttempts,
			)
		}
		time.Sleep(lockRetryDelay)
	}
	file.Close()
	return nil, fmt.Errorf("trust: acquire lock %s: too many attempts", path)
}
