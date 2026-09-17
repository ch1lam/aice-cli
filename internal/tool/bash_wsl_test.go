package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/llm"
)

func wslTestBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Linux-side WSL lifecycle tests run with Unix Bash; native WSL needs a distribution")
	}
	shell, err := deps.FindNativeBash(deps.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return shell
}

func TestWSLBashProbeAndFallback(t *testing.T) {
	shell := wslTestBash(t)
	root := t.TempDir()
	// Emulate wslpath only; run the production probe through real Bash stdin.
	envFile := filepath.Join(root, "env.sh")
	if err := os.WriteFile(envFile, []byte("wslpath() { printf '%s' \"$2\"; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", envFile)
	workspacePath := filepath.Join(root, "space ' 中文")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "broken-bash")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nprintf 'WSL has no installed distributions' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"healthy", []string{shell}, shell},
		{"broken then healthy", []string{broken, shell}, shell},
		{"broken only", []string{broken}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			bash, err := newWSLBash(t.Context(), workspace, test.paths, errors.New("native missing"))
			if test.want == "" {
				if err == nil || !strings.Contains(err.Error(), "no installed distributions") {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bash.wsl || bash.shellPath != test.want {
				t.Fatalf("Bash = %+v", bash)
			}
			result, err := bash.Execute(t.Context(), llm.ToolCall{ID: "wsl-cwd", Name: "bash",
				Arguments: []byte(`{"command":"printf ready > marker.txt; printf success"}`)})
			if err != nil || result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "success") {
				t.Fatalf("Execute = %+v, %v", result, err)
			}
			if data, err := os.ReadFile(filepath.Join(workspacePath, "marker.txt")); err != nil || string(data) != "ready" {
				t.Fatalf("workspace marker = %q, %v", data, err)
			}
		})
	}
	t.Run("workspace inaccessible", func(t *testing.T) {
		if err := os.WriteFile(envFile, []byte("wslpath() { printf '/aice-no-such-directory'; }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := probeWSLBash(t.Context(), shell, root); err == nil {
			t.Fatal("probe accepted inaccessible workspace")
		}
	})
	t.Run("launcher does not execute input", func(t *testing.T) {
		inert := filepath.Join(root, "inert-bash")
		if err := os.WriteFile(inert, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := probeWSLBash(t.Context(), inert, root); err == nil {
			t.Fatal("probe accepted exit zero without executing Bash input")
		}
	})
	t.Run("path translation failure", func(t *testing.T) {
		if err := os.WriteFile(envFile, []byte("wslpath() { return 1; }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := probeWSLBash(t.Context(), shell, root); err == nil {
			t.Fatal("probe accepted failed path conversion")
		}
	})
	t.Run("probe bounded by caller", func(t *testing.T) {
		hung := filepath.Join(root, "hung-bash")
		if err := os.WriteFile(hung, []byte("#!/bin/sh\nexec "+quoteBash(shell)+" -c 'while :; do :; done'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := newWSLBash(ctx, workspace, []string{hung}, errors.New("native missing"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestWSLCommandStdinTransport(t *testing.T) {
	shell := wslTestBash(t)
	for _, test := range []struct {
		name, script, output string
		exit                 int
	}{
		{"quoting and multiline", "name='world'; printf '%s\\n' \"hello $name\"\ncat <<'AICE_EOF'\n'quotes' \"double\" $(literal) \\ 中文\nAICE_EOF", "hello world\n'quotes' \"double\" $(literal) \\ 中文\n", 0},
		{"larger than pipe buffer", "printf '%s' '" + strings.Repeat("abcdef", 16000) + "'", strings.Repeat("abcdef", 16000), 0},
		{"exit status", "printf failure; exit 7", "failure", 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			output := newBoundedWriter(300000)
			err := runWSLCommand(ctx, shell, t.TempDir(), test.script, output)
			if test.exit == 0 && err != nil {
				t.Fatalf("error: %v; output: %s", err, output.String())
			}
			if test.exit != 0 {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != test.exit {
					t.Fatalf("error = %v", err)
				}
			}
			if got := output.String(); got != test.output {
				t.Fatalf("output length %d, want %d; tail=%q", len(got), len(test.output), got[max(0, len(got)-200):])
			}
		})
	}
}

func TestWSLCommandCancellationKillsLinuxGroup(t *testing.T) {
	shell := wslTestBash(t)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		defer close(result)
		result <- runWSLCommand(ctx, shell, root,
			"(sleep 2; printf survived > child.txt) & printf ready > ready.txt; wait", newBoundedWriter(1024))
	}()
	t.Cleanup(func() { cancel(); <-result })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
ready:
	for {
		select {
		case err := <-result:
			t.Fatalf("command ended before readiness: %v", err)
		case <-deadline.C:
			cancel()
			<-result
			t.Fatal("command never became ready")
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(root, "ready.txt")); err == nil {
				break ready
			}
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-deadline.C:
		t.Fatal("cancelled command hung")
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("Linux descendant survived: %v", err)
	}
}

func TestWSLCommandTimeoutStopsUnresponsiveLauncher(t *testing.T) {
	wslTestBash(t)
	root := t.TempDir()
	launcher := filepath.Join(root, "unresponsive-launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runWSLCommand(ctx, launcher, root, "printf never", newBoundedWriter(1024))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("unresponsive launcher cleanup took %s", elapsed)
	}
}
