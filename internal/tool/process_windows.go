//go:build windows

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureProcess(command *exec.Cmd) {
	command.WaitDelay = time.Second
	command.Cancel = nil
}

func supportsProcessTreeTermination() bool {
	return true
}

// treeKill terminates the whole process tree of one command. Cancel must be
// assigned before Start, because exec.Cmd reads it from the watchCtx goroutine
// launched by Start; job assignment only needs the pid, so it can happen after
// Start without touching Cancel.
type treeKill struct {
	job      windows.Handle
	assigned atomic.Bool
}

// cancel returns the Cancel callback. It kills the job object when the
// process tree was assigned to it, and falls back to taskkill when the job is
// empty (assignment failed or the process was never placed in the job).
func (k *treeKill) cancel(command *exec.Cmd) func() error {
	return func() error {
		if k.assigned.Load() {
			if err := windows.TerminateJobObject(k.job, 1); err == nil {
				return nil
			}
		}
		if command.Process == nil {
			return os.ErrProcessDone
		}
		kill := exec.Command(
			"taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F",
		)
		if err := kill.Run(); err != nil {
			return os.ErrProcessDone
		}
		return nil
	}
}

// startProcessTree starts command and arranges for cancellation to terminate
// the whole process tree. The returned cleanup closes the job handle once the
// process has exited.
func startProcessTree(command *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("tool: create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("tool: configure job object: %w", err)
	}

	kill := &treeKill{job: job}
	command.Cancel = kill.cancel(command)

	if err := command.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err == nil {
		if assignErr := windows.AssignProcessToJobObject(job, process); assignErr == nil {
			kill.assigned.Store(true)
		}
		windows.CloseHandle(process)
	}
	return func() { windows.CloseHandle(job) }, nil
}
