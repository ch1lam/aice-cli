//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockWriter(file *os.File) error {
	// A byte beyond practical EOF coordinates writers without denying reads
	// of transcript bytes. Windows releases it on handle close/process exit.
	offset := windows.Overlapped{Offset: 0xfffffffe, OffsetHigh: 0x7fffffff}
	err := windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	if err != nil {
		return fmt.Errorf("session: lock writer: %w", err)
	}
	return nil
}

func closeWriter(file *os.File) error {
	return file.Close()
}
