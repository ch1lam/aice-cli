package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the real command catalog through the CLI and Bubble Tea input path.
func TestSettingsUsageTUI(t *testing.T) {

	home, err := os.MkdirTemp("", "ab-tui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("AGENT_BROWSER_HEADED", "")
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"old-model"}`)
	installCalls, setupCalls := 0, 0
	command, err := newTestCommand(t, dependencies{
		loadConfig:   func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:     func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
		userHomeDir:  func() (string, error) { return home, nil },
		saveSettings: config.SaveSettingsFile,
		newDesktop: func(c config.Config) (*desktopState, error) {
			return &desktopState{installOptions: deps.DefaultOptions().WithNoInstall(c.NoDepInstall),
				install: func(_ context.Context, o deps.Options) (deps.CuaInstallResult, error) {
					installCalls++
					if !o.NoInstall {
						t.Error("startup download policy lost")
					}
					return deps.CuaInstallResult{Reused: true, Installation: deps.CuaInstallation{Binary: "/synthetic/driver"}}, nil
				},
				setup: func(context.Context, string) (desktop.SetupResult, error) {
					setupCalls++
					return desktop.SetupResult{AuthorizationRequested: true, AuthorizationCompleted: true, Ready: true}, nil
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bound the complete menu flow, including rendering under race instrumentation.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	reader, input := io.Pipe()
	defer reader.Close()
	defer input.Close()
	stopClosing := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()); input.CloseWithError(ctx.Err()) })
	defer stopClosing()
	output := loginTerminalOutput{ctx: ctx, frames: make(chan string, 256)}
	command.SetIn(reader)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"--workspace", t.TempDir(), "--no-approve", "--no-dep-install", "--no-update-check"})
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); input.Close(); reader.Close(); <-stopped })
	width := 120
	var transcript strings.Builder
	waitFor := func(want string) {
		t.Helper()
		var recent strings.Builder
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := io.WriteString(input, fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
					t.Fatal(err)
				}
				width = 239 - width
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				transcript.WriteString(plain)
				recent.WriteString(plain)
				if strings.Contains(recent.String(), want) {
					return
				}
			case err := <-done:
				t.Fatalf("command stopped while waiting for %q: %v\n%s", want, err, transcript.String())
			case <-ctx.Done():
				t.Fatalf("terminal never displayed %q\n%s", want, transcript.String())
			}
		}
	}
	// Supply a terminal size and force complete frames instead of matching cell diffs.
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 239 - width
	}
	send("")
	waitFor("AICE")

	send("/settings\r")
	waitFor("Models & Accounts")
	send("/Run timeout")
	waitFor("Run timeout")
	send("\r")
	waitFor("0s")
	send("\x151m30.000000001s\r")
	waitFor("Saved to user settings")
	send("\x1b")
	if runtime.GOOS == "darwin" {
		send("/settings\r")
		waitFor("Models & Accounts")
		send("/Computer Use setup")
		waitFor("Computer Use setup / repair")
		send("\r")
		waitFor("Enable preference only")
		send("\r")
		waitFor("outside this project")
		waitFor("Continue?")
		send("\x1b[B\r")
		waitFor("Computer Use enabled for the next run")
		send("\x1b")
		send("\x1b")
	}
	send("/usage\r")
	waitFor("Session usage")
	send("\x1b")
	send("/session\r")
	waitFor("Not started")
	send("\x1b")

	send("\x15/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command did not quit")
	}
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunTimeout != time.Minute+30*time.Second+time.Nanosecond {
		t.Fatalf("timeout not persisted: %v", loaded.RunTimeout)
	}
	if runtime.GOOS == "darwin" && (!loaded.DesktopEnabled || installCalls != 1 || setupCalls != 1) {
		t.Fatalf("desktop setup enabled=%v install=%d setup=%d", loaded.DesktopEnabled, installCalls, setupCalls)
	}
}
