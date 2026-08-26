//go:build windows

package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestConfigureProcessWindows(t *testing.T) {
	if !supportsProcessTreeTermination() {
		t.Fatal("supportsProcessTreeTermination() = false on windows")
	}
	command := exec.CommandContext(context.Background(), "cmd", "/c", "echo ok")
	configureProcess(command)
	if command.WaitDelay != time.Second {
		t.Fatalf("WaitDelay = %v, want 1s", command.WaitDelay)
	}
	cleanup, err := startProcessTree(command)
	if err != nil {
		t.Fatalf("startProcessTree() error = %v", err)
	}
	defer cleanup()
	if command.Cancel == nil {
		t.Fatal("startProcessTree did not set Cancel")
	}
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		t.Fatal("startProcessTree did not set CREATE_SUSPENDED")
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestStartProcessTreeKillsGrandchildOnCancel(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "spawn.bat")
	body := "@echo off\r\n" +
		`start /b "" cmd /c "ping -n 5 127.0.0.1 >nul & echo survived > child.txt"` +
		"\r\nping -n 20 127.0.0.1 >nul\r\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "cmd", "/c", script)
	command.Dir = dir
	configureProcess(command)

	started := time.Now()
	cleanup, err := startProcessTree(command)
	if err != nil {
		t.Fatalf("startProcessTree() error = %v", err)
	}
	defer cleanup()

	waitErr := command.Wait()
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Wait() took %s, want process tree killed promptly", elapsed)
	}
	if waitErr == nil {
		t.Fatal("Wait() error = nil, want cancellation after timeout")
	}

	if remaining := 6*time.Second - time.Since(started); remaining > 0 {
		time.Sleep(remaining)
	}
	if _, err := os.Stat(filepath.Join(dir, "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("grandchild survived cancellation, os.Stat() error = %v", err)
	}
}

func TestTreeKillCancelFallsBackToTaskkill(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("CreateJobObject() error = %v", err)
	}
	defer windows.CloseHandle(job)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, "cmd", "/c", "ping -n 20 127.0.0.1 >nul")
	configureProcess(command)
	kill := &treeKill{job: job}
	command.Cancel = kill.cancel(command)

	started := time.Now()
	if err := command.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	cancel()
	if err := command.Wait(); err == nil {
		t.Fatal("Wait() error = nil, want taskkill to terminate the process")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Wait() took %s, want taskkill to terminate promptly", elapsed)
	}
}
