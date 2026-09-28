package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Real Cobra + Bubble Tea input/rendering, with only a loopback MCP endpoint.
func TestMCPManagementTUI(t *testing.T) {
	endpoint, initialized, calls, closed := mcpStartupServer(t)
	paths := authTestPaths(t)
	home, err := os.MkdirTemp("", "am-tui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"fixture"}`)
	command, err := newTestCommand(t, dependencies{
		userHomeDir: func() (string, error) { return home, nil },
		loadConfig:  func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:    func(config.Config) (llm.Streamer, error) { return &recordingModel{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	reader, input := io.Pipe()
	defer reader.Close()
	defer input.Close()
	stop := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()); input.CloseWithError(ctx.Err()) })
	defer stop()
	output := loginTerminalOutput{ctx: ctx, frames: make(chan string, 256)}
	command.SetIn(reader)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"--workspace", t.TempDir(), "--no-approve", "--no-dep-install", "--no-update-check"})
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); input.Close(); reader.Close(); <-stopped })
	width := 140
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;50;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 279 - width
	}
	var transcript strings.Builder
	tail := func() string { text := transcript.String(); return text[max(0, len(text)-10000):] }
	waitFor := func(want string) {
		t.Helper()
		var recent strings.Builder
		tick := time.NewTicker(150 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				send("")
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				transcript.WriteString(plain)
				recent.WriteString(plain)
				if strings.Contains(recent.String(), want) {
					return
				}
			case err := <-done:
				t.Fatalf("stopped awaiting %q: %v\n%s", want, err, tail())
			case <-ctx.Done():
				t.Fatalf("missing %q\n%s", want, tail())
			}
		}
	}
	send("")
	waitFor("AICE")
	send("/mcp add docs\r")
	waitFor("MCP server definition (JSON)")
	send(`{"transport":"http","url":"` + endpoint + `"}` + "\r")
	waitFor("MCP configuration saved without connecting")
	if initialized.Load() != 0 {
		t.Fatal("add implicitly connected")
	}
	send("/settings\r")
	waitFor("Models & Accounts")
	send("/MCP services")
	waitFor("MCP services and authorization")
	send("\r")
	waitFor("Approve a connection")
	send("\x1b[B\x1b[B\x1b[B\r")
	waitFor("Select the exact MCP service")
	send("\r")
	waitFor("Confirm MCP approve")
	waitFor("Connection fingerprint:")
	send("\x1b[B\r")
	waitFor("MCP connection decision saved")
	send("\x1b")
	send("\x1b")
	send("/mcp connect user:docs\r")
	waitFor("MCP initialization and tool discovery succeeded")
	send("/mcp reconnect user:docs\r")
	waitFor("MCP reconnect completed.")
	send("/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("did not quit")
	}
	if initialized.Load() != 2 || closed.Load() != 2 || calls.Load() != 0 {
		t.Fatalf("connections=%d cleanup=%d tool calls=%d", initialized.Load(), closed.Load(), calls.Load())
	}
	loaded, err := config.LoadFiles(paths, config.LoadOptions{})
	if err != nil || loaded.MCP.ConnectionDecision("user:docs") != config.MCPConnectionAllow {
		t.Fatal("approval not persisted", err)
	}
}
