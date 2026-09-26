//go:build windows

package desktop

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryDesktopLock(file *os.File) error {
	offset := windows.Overlapped{}
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errDesktopOccupied
	}
	return err
}

func closeDesktopLock(file *os.File) error { return file.Close() }
