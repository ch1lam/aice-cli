//go:build windows

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
