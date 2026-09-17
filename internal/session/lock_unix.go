//go:build unix

package session

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// The descriptor owns the lock until Close; reads remain available to browsers.
func lockWriter(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrBusy
	}
	if err != nil {
		return fmt.Errorf("session: lock writer: %w", err)
	}
	return nil
}
