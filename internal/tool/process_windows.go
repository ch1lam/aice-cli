//go:build windows

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NtResumeProcess is not in the public Win32 API. It has been exported from
// ntdll since Windows XP and resumes every thread in a process, which lets
// CREATE_SUSPENDED hand-off avoid CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD).
// That snapshot API ignores its PID argument and enumerates every thread on
// the machine.
var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func configureProcess(command *exec.Cmd) {
	command.WaitDelay = time.Second
	command.Cancel = nil
}

func supportsProcessTreeTermination() bool {
	return true
}

// treeKill terminates the whole process tree of one command. Cancel must be
// assigned before Start, because exec.Cmd reads it from the watchCtx goroutine
// launched by Start.
type treeKill struct {
	job      windows.Handle
	assigned atomic.Bool
}

// cancel returns the Cancel callback. It kills the job object when the
// process tree was assigned to it, and falls back to taskkill when the job is
// empty (assignment has not completed, or TerminateJobObject failed).
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
//
// The process is created suspended, assigned to the job, then resumed with
// NtResumeProcess. AssignProcessToJobObject does not associate children that
// already exist; a running Start-then-Assign sequence leaves a window where
// bash can spawn a grandchild that survives TerminateJobObject. Assignment
// failure is fatal: the still-suspended process is killed rather than resumed
// outside the job.
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

	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED

	kill := &treeKill{job: job}
	command.Cancel = kill.cancel(command)

	if err := command.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}

	var assignErr, resumeErr error
	handleErr := command.Process.WithHandle(func(handle uintptr) {
		process := windows.Handle(handle)
		assignErr = windows.AssignProcessToJobObject(job, process)
		if assignErr != nil {
			return
		}
		kill.assigned.Store(true)
		resumeErr = resumeProcess(process)
	})
	if handleErr != nil {
		return failStartedProcessTree(command, job, fmt.Errorf("tool: process handle: %w", handleErr))
	}
	if assignErr != nil {
		return failStartedProcessTree(command, job, fmt.Errorf("tool: assign process to job object: %w", assignErr))
	}
	if resumeErr != nil {
		return failStartedProcessTree(command, job, fmt.Errorf("tool: resume process: %w", resumeErr))
	}
	return func() { windows.CloseHandle(job) }, nil
}

func failStartedProcessTree(command *exec.Cmd, job windows.Handle, err error) (func(), error) {
	if command.Process != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
	windows.CloseHandle(job)
	return nil, err
}

func resumeProcess(process windows.Handle) error {
	if err := procNtResumeProcess.Find(); err != nil {
		return err
	}
	status, _, _ := procNtResumeProcess.Call(uintptr(process))
	if status != 0 {
		return windows.NTStatus(status)
	}
	return nil
}
