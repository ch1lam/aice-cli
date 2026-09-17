//go:build !unix && !windows

package session

import (
	"fmt"
	"os"
)

func lockWriter(*os.File) error {
	return fmt.Errorf("session: writer locking is unsupported on this platform")
}
