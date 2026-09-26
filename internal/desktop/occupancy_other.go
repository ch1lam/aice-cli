//go:build !unix && !windows

package desktop

import (
	"errors"
	"os"
)

func tryDesktopLock(*os.File) error {
	return errors.New("desktop occupancy locking is unsupported on this platform")
}
func closeDesktopLock(file *os.File) error { return file.Close() }
