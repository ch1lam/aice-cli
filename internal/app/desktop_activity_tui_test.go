package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/desktop"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// Real command -> Loop -> typed tool -> event projection -> Bubble Tea. The
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
			return &desktopState{bind: func(context.Context, desktop.RunOptions) (tool.DesktopBackend, func() error, error) {
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
				if strings.Contains(plain, "PRIVATE CONDITION") {
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
	waitFor("Computer Use · Notes · Waiting")
	close(backend.release)
	waitFor("Computer Use · Notes · Planning")
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

type activityDesktopBackend struct{ release chan struct{} }

func (*activityDesktopBackend) Apps(context.Context, string, int) (desktop.Discovery, error) {
	return desktop.Discovery{Windows: []desktop.Window{{Ref: "w", App: "Notes"}}}, nil
}
func (*activityDesktopBackend) Observe(context.Context, desktop.ObserveRequest) (desktop.Observation, error) {
	return desktop.Observation{Ref: "o", TargetRef: "w"}, nil
}
func (b *activityDesktopBackend) Act(ctx context.Context, _ desktop.ActRequest) (desktop.ActResult, error) {
	select {
	case <-ctx.Done():
		return desktop.ActResult{}, ctx.Err()
	case <-b.release:
		return desktop.ActResult{Outcome: "returned", WaitState: "satisfied", Observation: &desktop.Observation{Ref: "next", TargetRef: "w"}}, nil
	}
}

type activityDesktopModel struct{ count int }

func (m *activityDesktopModel) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	calls := []llm.ToolCall{
		{ID: "apps", Name: "desktop_apps", Arguments: []byte(`{}`)},
		{ID: "observe", Name: "desktop_observe", Arguments: []byte(`{"target_ref":"w"}`)},
		{ID: "act", Name: "desktop_act", Arguments: []byte(`{"action":"wait","observation_ref":"o","wait":{"text":"PRIVATE CONDITION","timeout_ms":1000}}`)},
	}
	index := m.count
	m.count++
	if index < len(calls) {
		return toolCallEventStream(req.Model, calls[index]), nil
	}
	return &gatedStream{ctx: ctx, release: make(chan struct{}), prefix: []llm.Event{{Type: llm.EventTypeStart}}}, nil
}
