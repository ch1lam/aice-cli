package tool_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestBashExecuteRunsWithoutApproval(t *testing.T) {
	t.Parallel()
	workspace, _ := newWorkspace(t)
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("NewBash() error = %v", err)
	}
	result, err := bash.Execute(
		t.Context(),
		toolCall(t, "bash", map[string]any{"command": "printf ready"}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError || !strings.Contains(resultText(t, result), "ready") {
		t.Fatalf("Execute() result = %#v", result)
	}
}

func TestBashExecuteRejectsInvalidTimeoutBeforeStartingProcess(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("NewBash() error = %v", err)
	}
	_, err = bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": "printf ran > marker.txt",
		"timeout": -1,
	}))
	if err == nil || !strings.Contains(err.Error(), "timeout must be positive") {
		t.Fatalf("Execute() error = %v, want timeout validation error", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "marker.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("marker os.Stat() error = %v, want not exist", statErr)
	}
}

func TestBashExecuteUsesWorkingDirectoryAndHostEnvironment(t *testing.T) {
	workspace, root := newWorkspace(t)
	t.Setenv("AICE_TOOL_TEST_VALUE", "inherited")
	t.Setenv("AICE_TOOL_TEST_SECRET", "hidden")
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("NewBash() error = %v", err)
	}

	result, err := bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": `printf '%s' "$AICE_TOOL_TEST_VALUE"; printf cwd > working-directory-marker.txt`,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "inherited") {
		t.Fatalf("Execute() text = %q", text)
	}
	marker, err := os.ReadFile(filepath.Join(root, "working-directory-marker.txt"))
	if err != nil {
		t.Fatalf("ReadFile(working-directory-marker.txt) error = %v", err)
	}
	if got, want := string(marker), "cwd"; got != want {
		t.Fatalf("working directory marker = %q, want %q", got, want)
	}
	if strings.Contains(text, "hidden") {
		t.Fatalf("Execute() leaked an environment value it was not asked to print: %q", text)
	}
	if result.IsError {
		t.Fatalf("Execute() result IsError = true, text = %q", text)
	}
}

func TestBashExecuteReportsExitAndTimeout(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("NewBash() error = %v", err)
	}

	result, err := bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{"command": "printf failure; exit 7"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "exit code: 7") {
		t.Fatalf("Execute() result = %#v", result)
	}

	started := time.Now()
	result, err = bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": "(sleep 3; printf survived > child.txt) & wait",
		"timeout": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() timeout error = %v", err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "timed out") {
		t.Fatalf("Execute() timeout result = %#v", result)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Execute() timeout took %s, want process tree killed promptly", elapsed)
	}
	if remaining := 4*time.Second - time.Since(started); remaining > 0 {
		time.Sleep(remaining)
	}
	if _, statErr := os.Stat(filepath.Join(root, "child.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("child process survived cancellation, os.Stat() error = %v", statErr)
	}
}

func TestBashExecuteBoundsCombinedOutput(t *testing.T) {
	t.Parallel()
	workspace, _ := newWorkspace(t)
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("NewBash() error = %v", err)
	}
	result, err := bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
		"command": "for ((i=0; i<60000; i++)); do printf x; done",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if len(text) > 50*1024 || !strings.Contains(text, "[output truncated]") {
		t.Fatalf("Execute() output length = %d", len(text))
	}
}

func TestBashExecuteRetainsFinalDiagnostic(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		ending  string
		status  string
		isError bool
	}{
		{name: "success", ending: "exit 0", status: "exit code: 0"},
		{name: "failure", ending: "exit 7", status: "exit code: 7", isError: true},
		{name: "timeout", ending: "sleep 5", status: "timed out", isError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workspace, _ := newWorkspace(t)
			bash, err := tool.NewBash(workspace)
			if err != nil {
				t.Skipf("NewBash() error = %v", err)
			}
			result, err := bash.Execute(t.Context(), toolCall(t, "bash", map[string]any{
				"command": "printf 'HEAD\\n'; printf '%060000d' 0; printf '\\nFINAL DIAGNOSTIC\\n' >&2; " + test.ending,
				"timeout": 1,
			}))
			if err != nil {
				t.Fatal(err)
			}
			text := resultText(t, result)
			if len(text) > 50*1024 || result.IsError != test.isError ||
				!strings.HasPrefix(text, "HEAD\n") || !strings.Contains(text, "[output truncated]") ||
				!strings.Contains(text, "FINAL DIAGNOSTIC") || !strings.Contains(text, test.status) {
				t.Fatalf("missing bounded head/tail/status: %d bytes, isError=%v, tail=%q", len(text), result.IsError, text[max(0, len(text)-100):])
			}
		})
	}
}

func TestBashExecuteHonorsCallerCancellation(t *testing.T) {
	t.Parallel()
	workspace, _ := newWorkspace(t)
	bash, err := tool.NewBash(workspace)
	if err != nil {
		t.Skipf("NewBash() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	_, err = bash.Execute(ctx, toolCall(t, "bash", map[string]any{
		"command": "printf '%060000d' 0; sleep 10",
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}
