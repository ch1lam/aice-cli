//go:build windows

package tool

import (
	"context"
	"os/exec"
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
