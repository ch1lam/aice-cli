//go:build windows

package tool

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestConfigureProcessWindows(t *testing.T) {
	if !supportsProcessTreeTermination() {
		t.Fatal("supportsProcessTreeTermination() = false on windows")
	}
	command := exec.CommandContext(context.Background(), "cmd", "/c", "echo ok")
	configureProcess(command)
	if command.Cancel == nil {
		t.Fatal("configureProcess did not set Cancel")
	}
	if command.WaitDelay != time.Second {
		t.Fatalf("WaitDelay = %v, want 1s", command.WaitDelay)
	}
}
