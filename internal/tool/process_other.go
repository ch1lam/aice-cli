//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package tool

import (
	"os/exec"
	"time"
)

func configureProcess(command *exec.Cmd) {
	command.WaitDelay = time.Second
}

func supportsProcessTreeTermination() bool {
	return false
}
