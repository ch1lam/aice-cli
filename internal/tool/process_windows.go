//go:build windows

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

// startProcessTree starts command and assigns it to a job object so that
// cancellation terminates the whole process tree at once. It returns a
// cleanup function that closes the job handle once the process has exited.
func startProcessTree(command *exec.Cmd) (func(), error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
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
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("tool: open process for job assignment: %w", err)
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if assignErr != nil {
		windows.CloseHandle(job)
		command.Cancel = taskkillCancel(command)
		return nil, nil
	}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := windows.TerminateJobObject(job, 1); err != nil {
			windows.CloseHandle(job)
			return err
		}
		return nil
	}
	return func() { windows.CloseHandle(job) }, nil
}

// taskkillCancel is the fallback tree kill for processes that cannot be
// assigned to a job object, for example when an enclosing job already
// restricts the process.
func taskkillCancel(command *exec.Cmd) func() error {
	return func() error {
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
