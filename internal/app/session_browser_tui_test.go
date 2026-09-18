package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Exercise history through the real CLI and Bubble Tea, using generated prose
// and isolated settings. No provider request or user conversation is needed.
func TestSessionBrowserTUI(t *testing.T) {
	s := browserHarness(t)
	for _, fixture := range []struct{ key, answer string }{
		{"target", strings.Repeat("- History list item with **bold** 中文\n", 1000) + "\nİ中文 NEEDLE found in history"},
		{"other", "Another conversation without the search term"},
	} {
		store := browserFixture(t, s, fixture.key, fixture.key+" question", fixture.answer, 100)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"test-model"}`)
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:   func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
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
	command.SetArgs([]string{"--workspace", s.workspace.Path(), "--no-approve", "--no-dep-install", "--no-update-check"})
	done, stopped := make(chan error, 1), make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); input.Close(); reader.Close(); <-stopped })
	waitFor := func(want string) {
		t.Helper()
		var frames strings.Builder
		for {
			select {
			case frame := <-output.frames:
				frames.WriteString(ansi.Strip(frame))
				if strings.Contains(frames.String(), want) {
					return
				}
			case err := <-done:
				t.Fatalf("command stopped waiting for %q: %v\n%s", want, err, frames.String())
			case <-ctx.Done():
				t.Fatalf("missing %q\n%s", want, frames.String())
			}
		}
	}
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
	send("/history\r")
	waitFor("target question")
	send("/i中文 needle")
	waitFor("NEEDLE found in history")
	send("\x1b[C")
	waitFor("Last activity")
	send("\x1b")
	waitFor("CURRENT PROJECT")
	send("\x1bOS") // F4: read without replacing the live conversation.
	waitFor("HISTORY · READ ONLY")
	send("\x1b[5~") // PageUp into the long list.
	waitFor("History list item")
	send("\x1b")
	waitFor("CURRENT PROJECT")
	send("\r")
	waitFor("Resumed session")
	send("/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command did not quit")
	}
}
