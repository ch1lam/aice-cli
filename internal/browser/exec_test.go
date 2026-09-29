package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecuteHelperProcess(t *testing.T) {
	switch os.Getenv("AICE_BROWSER_EXEC_TEST") {
	case "success":
		fmt.Print(`{"success":true}`)
	case "failure":
		fmt.Fprint(os.Stderr, "helper failure")
		os.Exit(7)
	case "stdout boundary":
		fmt.Print(strings.Repeat("x", 1<<20))
	case "stdout overflow":
		fmt.Print(strings.Repeat("x", (1<<20)+1))
	case "stderr boundary":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 4096))
		os.Exit(7)
	case "stderr overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 4096)+"overflow")
		os.Exit(7)
	case "cancel":
		if err := os.WriteFile(os.Getenv("AICE_BROWSER_EXEC_READY"), nil, 0600); err != nil {
			os.Exit(8)
		}
		time.Sleep(time.Minute)
	default:
		return
	}
	os.Exit(0)
}

func TestExecuteReportsProcessStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed Browser is unavailable on Windows")
	}
	for _, name := range []string{
		"success", "failure", "cancel before start", "cancel", "invalid executable", "missing helper",
		"stdout boundary", "stdout overflow", "stderr boundary", "stderr overflow",
	} {
		t.Run(name, func(t *testing.T) {
			m := testManager(t)
			path := filepath.Join(m.binDir, "agent-browser")
			if name != "invalid executable" {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(executable)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if name == "missing helper" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if name == "cancel before start" {
				cancel()
			}
			ready := filepath.Join(m.workspace, "ready")
			env := append(os.Environ(), "AICE_BROWSER_EXEC_TEST="+name, "AICE_BROWSER_EXEC_READY="+ready)
			cancelDone := make(chan struct{})
			if name == "cancel" {
				go func() {
					defer close(cancelDone)
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							if _, err := os.Stat(ready); err == nil {
								cancel()
								return
							}
						}
					}
				}()
			} else {
				close(cancelDone)
			}
			data, started, err := m.execute(ctx, []string{"-test.run=^TestExecuteHelperProcess$"}, env)
			cancel()
			<-cancelDone
			wantStarted := name != "cancel before start" && name != "invalid executable" && name != "missing helper"
			wantSuccess := name == "success" || name == "stdout boundary"
			if started != wantStarted || (err == nil) != wantSuccess {
				t.Fatalf("started=%v has error=%v output bytes=%d", started, err != nil, len(data))
			}
			switch name {
			case "success":
				if string(data) != `{"success":true}` {
					t.Fatalf("output=%q", data)
				}
			case "failure":
				if !strings.Contains(err.Error(), "helper failure") {
					t.Fatal(err)
				}
			case "stdout boundary":
				if string(data) != strings.Repeat("x", 1<<20) {
					t.Fatalf("exact-boundary output changed: %d bytes", len(data))
				}
			case "stdout overflow":
				if err.Error() != "agent-browser response exceeds 1 MiB" || data != nil {
					t.Fatalf("overflow response: error=%v output bytes=%d", err, len(data))
				}
			case "stderr boundary", "stderr overflow":
				if err.Error() != "agent-browser: exit status 7: "+strings.Repeat("x", 4096) {
					t.Fatalf("stderr failure diagnostic not capped at 4096 bytes: %d bytes", len(err.Error()))
				}
			case "cancel", "cancel before start":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "missing helper":
				if !errors.Is(err, ErrHelperMissing) {
					t.Fatal(err)
				}
			}
		})
	}
}
