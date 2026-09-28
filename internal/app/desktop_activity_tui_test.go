package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/mcpclient"
)

// Real command -> Loop -> selected MCP tool -> event projection -> Bubble Tea. The
// only desktop is this synthetic backend; no native helper or model is used.
func TestDesktopActivityTUI(t *testing.T) {
	home := t.TempDir()
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"synthetic","desktop_enabled":true}`)
	backend := &activityDesktopBackend{release: make(chan struct{})}
	model := &activityDesktopModel{}
	command, err := newTestCommand(t, dependencies{
		loadConfig:  func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:    func(config.Config) (llm.Streamer, error) { return model, nil },
		userHomeDir: func() (string, error) { return home, nil },
		newDesktop: func(config.Config) (*desktopState, error) {
			return &desktopState{bind: func(context.Context, desktop.RunOptions) (managedDesktopRun, func() error, error) {
				return backend, func() error { return nil }, nil
			}}, nil
		},
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
	command.SetArgs([]string{"--workspace", t.TempDir(), "--no-approve", "--no-dep-install", "--no-update-check"})
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); reader.Close(); input.Close(); <-stopped })
	width := 120
	send := func(text string) {
		t.Helper()
		if _, err := io.WriteString(input, text+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 239 - width
	}
	waitFor := func(text string) {
		t.Helper()
		tick := time.NewTicker(150 * time.Millisecond)
		defer tick.Stop()
		var recent strings.Builder
		for {
			select {
			case <-tick.C:
				send("")
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				if strings.Contains(plain, "PRIVATE INPUT") {
					t.Fatal("folded input leaked into default UI")
				}
				recent.WriteString(plain)
				if strings.Contains(recent.String(), text) {
					return
				}
			case err := <-done:
				t.Fatalf("command stopped before %q: %v", text, err)
			case <-ctx.Done():
				t.Fatalf("missing %q: %s", text, recent.String())
			}
		}
	}
	send("")
	waitFor("AICE")
	send("inspect synthetic notes\r")
	waitFor("Computer Use · Background requested")
	close(backend.release)
	waitFor("Computer Use · Planning")
	send("/desktop\r")
	waitFor("Stop current run")
	send("\x1b[17~")
	send("\x1b")
	waitFor("Response cancelled")
	send("\x15/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command did not quit")
	}
}

type activityDesktopBackend struct {
	appDesktopBackend
	release chan struct{}
}

func (*activityDesktopBackend) Tools(context.Context) (mcpclient.Catalog[mcpclient.Tool], error) {
	definition := catalogFixtureTool("type_text", "Type text in a synthetic window")
	definition.InputSchema = json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)
	return mcpclient.Catalog[mcpclient.Tool]{Complete: true, Generation: 1, Items: []mcpclient.Tool{definition}}, nil
}

func (b *activityDesktopBackend) CallChecked(ctx context.Context, name string, raw json.RawMessage, check func(context.Context) error) (mcpclient.Result, error) {
	if err := check(ctx); err != nil {
		return mcpclient.Result{State: llm.ExecutionNotDispatched}, err
	}
	select {
	case <-ctx.Done():
		return mcpclient.Result{State: llm.ExecutionUnknown}, ctx.Err()
	case <-b.release:
		return b.appDesktopBackend.CallChecked(ctx, name, raw, check)
	}
}

type activityDesktopModel struct{ managedDesktopModel }

func (m *activityDesktopModel) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	m.operation = "type_text"
	m.arguments = json.RawMessage(`{"text":"PRIVATE INPUT"}`)
	if len(m.requests) < 2 {
		return m.managedDesktopModel.Stream(ctx, req)
	}
	return &gatedStream{ctx: ctx, release: make(chan struct{}), prefix: []llm.Event{{Type: llm.EventTypeStart}}}, nil
}
