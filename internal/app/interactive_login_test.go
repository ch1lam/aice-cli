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

// Exercise the real command catalog through the CLI and Bubble Tea input path.
func TestLoginTUI(t *testing.T) {
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"old-model"}`)
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:   func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
		saveAPIKey: func(_ string, key string) (string, error) {
			return paths.GlobalAuth, config.SaveCustomAPIKeyFile(paths, key)
		},
		saveSettings: config.SaveSettingsFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	// This bounds the entire multi-step flow, including per-key rendering.
	// Race-instrumented CI runners can still be typing when 15 seconds elapse.
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
	send("/login custom https://inline.example/v1\r")
	waitFor("No matching options")
	// Clear the draft, then confirm each real menu level separately.
	send("\x15/login\r")
	waitFor("SELECT AUTHENTICATION METHOD")
	send("API key\r")
	waitFor("SELECT API KEY PROVIDER")
	send("Custom\r")
	waitFor("CUSTOM CREDENTIAL")
	send("Enter a new\r")
	waitFor("Custom endpoint URL (e.g.")
	send("https://form.example/v1\r")
	waitFor("API key (leave empty for Ollama,")
	send("\r")
	waitFor("Model name (e.g.")
	send("form-model\r")
	waitFor("Configured Custom")
	send("/quit\r")
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
	if loaded.Provider != "custom" || loaded.CustomBaseURL != "https://form.example/v1" || loaded.Model != "form-model" || loaded.CustomAPIKey != "" {
		t.Fatal("menu configuration was not persisted")
	}
	if strings.Contains(transcript.String(), "/login custom [endpoint]") {
		t.Fatal("menu advertises removed shortcut")
	}
}

type loginTerminalOutput struct {
	ctx    context.Context
	frames chan string
}

func (o loginTerminalOutput) Write(p []byte) (int, error) {
	select {
	case o.frames <- string(p):
		return len(p), nil
	case <-o.ctx.Done():
		return 0, o.ctx.Err()
	}
}
