//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tool_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Stopping a task ends descendants that keep its output open, not only the
// shell AICE started.
func TestBackgroundTaskKillEndsDescendants(t *testing.T) {
	t.Parallel()
	tt := newTaskTools(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	id := tt.start(t, "sleep 60 & echo $! > "+pidFile+"; wait")
	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0 && time.Now().Before(deadline); {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("descendant pid was not written")
	}
	if _, err := tt.kill.Execute(t.Context(), toolCall(t, "task_kill", map[string]any{"task_id": id})); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	t.Fatalf("descendant %d survived task_kill", pid)
}
