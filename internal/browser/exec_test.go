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
	for _, name := range []string{"success", "failure", "cancel before start", "cancel", "invalid executable", "missing helper"} {
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
			wantStarted := name == "success" || name == "failure" || name == "cancel"
			if started != wantStarted || (err == nil) != (name == "success") {
				t.Fatalf("started=%v err=%v output bytes=%d", started, err, len(data))
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
