//go:build !windows

package browser

import "syscall"

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) != syscall.ESRCH }
