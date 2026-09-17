//go:build !unix && !windows

package session

import (
	"fmt"
	"os"
)

func lockWriter(*os.File) error {
	return fmt.Errorf("session: writer locking is unsupported on this platform")
}

func closeWriter(file *os.File) error {
	return file.Close()
}
