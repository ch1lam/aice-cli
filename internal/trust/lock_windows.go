//go:build windows

package trust

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireLock takes an exclusive byte-range lock on the lock file. The
// returned function releases the lock and closes the file. The call blocks
// until the lock is available, mirroring flock(2) on Unix. Windows releases
// byte-range locks automatically when the owning handle closes or the process
// exits, so a crashed process cannot leave a stale lock behind.
func acquireLock(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trust: open lock %s: %w", path, err)
	}
	var ol windows.Overlapped
	if err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0, &ol,
	); err != nil {
		file.Close()
		return nil, fmt.Errorf("trust: acquire lock %s: %w", path, err)
	}
	return func() error {
		return errors.Join(
			windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &ol),
			file.Close(),
		)
	}, nil
}
