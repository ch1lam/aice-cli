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
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the real command catalog through the CLI and Bubble Tea input path.
func TestBrowserWindowTUI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("browser unsupported on Windows")
	}
	home, err := os.MkdirTemp("", "ab-tui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("AGENT_BROWSER_HEADED", "")
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"old-model"}`)
	command, err := newTestCommand(t, dependencies{
		loadConfig:   func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:     func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
		userHomeDir:  func() (string, error) { return home, nil },
		saveSettings: config.SaveSettingsFile,
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
	var transcript strings.Builder
	waitFor := func(want string) {
		t.Helper()
		var recent strings.Builder
		for {
			select {
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
	width := 120
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 239 - width
	}
	send("")
	waitFor("AICE")
	send("/browser\r")
	waitFor("Show window: off")
	send("Show window\r")
	waitFor("Show window: on (saved)")
	send("/browser\r")
	waitFor("Show window: on (toggle)")
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
	if !loaded.BrowserHeaded {
		t.Fatal("window preference not persisted")
	}
	if got := os.Getenv("AGENT_BROWSER_HEADED"); got != "true" {
		t.Fatalf("browser launch environment = %q", got)
	}
}
