package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/deps"
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
	installCalls, grantCalls := 0, 0
	driver := filepath.Join(home, "cua-driver")
	model := &gatedModel{gates: map[int]chan struct{}{1: make(chan struct{})}, preDelta: "Synthetic run waiting"}
	command, err := newTestCommand(t, dependencies{
		loadConfig:   func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:     func(config.Config) (llm.Streamer, error) { return model, nil },
		userHomeDir:  func() (string, error) { return home, nil },
		saveSettings: config.SaveSettingsFile,
		newDesktop: func(c config.Config) *desktopPreset {
			return &desktopPreset{installOptions: deps.DefaultOptions().WithNoInstall(c.NoDepInstall),
				install: func(_ context.Context, o deps.Options) (deps.CuaInstallResult, error) {
					installCalls++
					if !o.NoInstall {
						t.Error("startup download policy lost")
					}
					return deps.CuaInstallResult{Reused: true, Installation: deps.CuaInstallation{Binary: driver}}, nil
				},
				grant: func(context.Context, string) error { grantCalls++; return nil },
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bound the complete menu flow, including rendering under race
	// instrumentation. The macOS runners are slower than local hosts, so the
	// bound leaves room for contention while still failing a hung flow.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	reader, input := io.Pipe()
	defer reader.Close()
	defer input.Close()
	stopClosing := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()); input.CloseWithError(ctx.Err()) })
	defer stopClosing()
	// Keep a deep frame buffer: the TUI blocks on output while the test may be
	// writing input, and resize repaints can otherwise fill the channel and
	// deadlock both sides of the pipes.
	output := loginTerminalOutput{ctx: ctx, frames: make(chan string, 4096)}
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
	transcriptTail := func() string {
		const limit = 8 << 10
		value := transcript.String()
		if len(value) > limit {
			return "...(earlier frames truncated)...\n" + value[len(value)-limit:]
		}
		return value
	}
	waitFor := func(want ...string) {
		t.Helper()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if len(output.frames) > 0 {
					// Drain rendered frames before asking for another repaint;
					// otherwise a blocked writer and a blocked input write can
					// deadlock until the test context expires.
					continue
				}
				if _, err := io.WriteString(input, fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
					t.Fatalf("repaint while waiting for %q: %v\n%s", want, err, transcriptTail())
				}
				width = 239 - width
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				transcript.WriteString(plain)
				// Resize repaints let all markers match the same frame. Combining
				// old transcript text with a later header can accept stale state.
				matched := true
				for _, marker := range want {
					matched = matched && strings.Contains(plain, marker)
				}
				if matched {
					return
				}
			case err := <-done:
				t.Fatalf("command stopped while waiting for %q: %v\n%s", want, err, transcriptTail())
			case <-ctx.Done():
				t.Fatalf("terminal never displayed %q\n%s", want, transcriptTail())
			}
		}
	}
	// Supply a terminal size and force complete frames instead of matching cell diffs.
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatalf("send %q: %v\n%s", value, err, transcriptTail())
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
	send("/desktop\r")
	waitFor("not set up", "Set up  Install the Driver")
	send("\r")
	waitFor("user:cua")
	waitFor("Continue?")
	send("\x1b[B\r")
	waitFor("Computer Use is available to the next Run")
	send("\x1b")
	send("\x1b")
	send("/usage\r")
	waitFor("Session usage")
	send("\x1b")
	send("/session\r")
	waitFor("Not started")
	send("\x1b")

	send("wait for explicit cancellation\r")
	waitFor("Synthetic run waiting")
	send("/desktop\r")
	waitFor("Stop current run")
	// Esc only closes the modal; the next open must still offer Stop.
	send("\x1b")
	waitFor("Synthetic run waiting")
	send("/desktop\r")
	waitFor("Stop current run")
	send("\x1b[17~") // F6: the explicit Settings stop control.
	send("\x1b")
	waitFor("Response cancelled", "● READY")

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
	server := loaded.MCP.Servers["user:cua"]
	if !server.Enabled || server.Settings.Command != driver || loaded.MCP.ConnectionDecision("user:cua") != config.MCPConnectionAllow || installCalls != 1 || grantCalls != 1 {
		t.Fatalf("desktop setup server=%+v approval=%s install=%d grant=%d", server.Settings, loaded.MCP.ConnectionDecision("user:cua"), installCalls, grantCalls)
	}
	if model.requestCount() != 1 {
		t.Fatalf("stop requests=%d", model.requestCount())
	}
}
