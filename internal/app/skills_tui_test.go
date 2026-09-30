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

// Exercise the real CLI, composer, Guard and skill tool with an offline model.
func TestSkillShortcutTUI(t *testing.T) {
	paths := authTestPaths(t)
	writeConfigFixture(t, paths.GlobalSettings, `{"provider":"custom","model":"test-model","custom_api_key":"fixture"}`)
	model := &recordingModel{response: "Skill attached successfully."}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(options config.LoadOptions) (config.Config, error) { return config.LoadFiles(paths, options) },
		newModel:   func(config.Config) (llm.Streamer, error) { return model, nil },
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
	t.Cleanup(func() { cancel(); input.Close(); reader.Close(); <-stopped })
	width := 140
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 279 - width
	}
	waitFor := func(want ...string) {
		t.Helper()
		// Repaint asynchronous updates so all markers can match one frame,
		// without combining an old idle header with a new streaming reply.
		repaint := time.NewTicker(250 * time.Millisecond)
		defer repaint.Stop()
		var recent strings.Builder
		for {
			select {
			case frame := <-output.frames:
				plain := ansi.Strip(frame)
				recent.WriteString(plain)
				matched := true
				for _, marker := range want {
					matched = matched && strings.Contains(plain, marker)
				}
				if matched {
					return
				}
			case <-repaint.C:
				send("")
			case err := <-done:
				t.Fatalf("command stopped waiting for %q: %v\n%s", want, err, recent.String())
			case <-ctx.Done():
				t.Fatalf("terminal never displayed %q\n%s", want, recent.String())
			}
		}
	}
	send("")
	waitFor("AICE")
	send("Please /create-skill")
	waitFor("/skill:create-skill")
	send("\t")
	waitFor("[skill:create-skill]")
	send("Create a tiny test skill. /help\r")
	waitFor("Available slash commands")
	send("\r")
	// /help also renders READY before submission. Wait for this reply and
	// the idle header together before /quit can be interpreted as a command.
	waitFor(model.response, "● READY")
	send("/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command did not quit")
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want one request with loaded skill", len(model.requests))
	}
	first, ok := model.requests[0].Messages[0].(llm.UserMessage)
	if !ok {
		t.Fatal("shortcut was not submitted as a user message")
	}
	if text := first.Content[0].Text; !strings.Contains(text, "Please [skill:create-skill]") || !strings.Contains(text, "Create a tiny test skill.") {
		t.Fatalf("lost explicit selection or task: %q", text)
	}
	if !strings.Contains(first.Content[0].Text, `<skill_content name="create-skill">`) {
		t.Fatal("skill body did not reach the first model request")
	}
}
