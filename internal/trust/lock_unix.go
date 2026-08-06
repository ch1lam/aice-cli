//go:build unix

package trust

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// acquireLock takes an exclusive advisory lock on the lock file. The returned
// function releases the lock and closes the file.
func acquireLock(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trust: open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("trust: acquire lock %s: %w", path, err)
	}
	return func() error {
		return errors.Join(
			syscall.Flock(int(file.Fd()), syscall.LOCK_UN),
			file.Close(),
		)
	}, nil
}
