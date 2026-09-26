//go:build unix

package desktop

import (
	"errors"
	"os"
	"syscall"
)

func tryDesktopLock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errDesktopOccupied
	}
	return err
}

func closeDesktopLock(file *os.File) error {
	return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
}
