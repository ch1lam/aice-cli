//go:build unix

package session

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// The open file description owns the lock; reads remain available to browsers.
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

func closeWriter(file *os.File) error {
	// A concurrently forked child can retain this description until exec,
	// despite close-on-exec. Unlock explicitly so Close releases ownership now.
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if err != nil {
		err = fmt.Errorf("session: unlock writer: %w", err)
	}
	return errors.Join(err, file.Close())
}
