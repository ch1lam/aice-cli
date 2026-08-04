//go:build windows

package tool

import (
	"os"
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(command *exec.Cmd) {
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		// taskkill /T /F terminates the child's whole process tree, the Windows
		// analogue of the POSIX process-group kill used on Unix. A nonzero exit
		// means the process is already gone; treat that as done rather than
		// injecting an error.
		kill := exec.Command(
			"taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F",
		)
		if err := kill.Run(); err != nil {
			return os.ErrProcessDone
		}
		return nil
	}
}

func supportsProcessTreeTermination() bool {
	return true
}
