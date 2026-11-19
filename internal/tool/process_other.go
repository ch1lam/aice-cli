//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package tool

import (
	"errors"
	"os/exec"
	"time"
)

func configureProcess(command *exec.Cmd) {
	command.WaitDelay = time.Second
}

func startProcessTree(command *exec.Cmd) (func(), error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return func() {}, nil
}

// interruptProcessTree has no graceful form here; callers kill the tree.
func interruptProcessTree(*exec.Cmd) error {
	return errors.ErrUnsupported
}

func supportsProcessTreeTermination() bool {
	return false
}
