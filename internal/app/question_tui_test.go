package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/config"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/charmbracelet/x/ansi"
)

// Exercise the CLI, application question bridge and real Bubble Tea input path
// with a scripted provider and isolated settings.
func TestQuestionTUI(t *testing.T) {
	backend := &toolLoopModel{firstCall: &llm.ToolCall{
		ID: "question-1", Name: "request_user_input", Arguments: []byte(`{"questions":[
		{"id":"mode","question":"Choose a mode","options":[{"id":"old","label":"Old choice"}]},
		{"id":"goal","question":"Describe the goal"}
	]}`),
	}}
	command, err := newTestCommand(t, dependencies{
		loadConfig: func(config.LoadOptions) (config.Config, error) { return config.Config{DeepSeekAPIKey: "test-key"}, nil },
		newModel:   func(config.Config) (llm.Streamer, error) { return backend, nil },
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
	done, stopped := make(chan error, 1), make(chan struct{})
	go func() { defer close(stopped); done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { cancel(); input.Close(); reader.Close(); <-stopped })
	width := 120
	send := func(value string) {
		t.Helper()
		if _, err := io.WriteString(input, value+fmt.Sprintf("\x1b[8;40;%dt", width)); err != nil {
			t.Fatal(err)
		}
		width = 239 - width
	}
	waitFor := func(want string) {
		t.Helper()
		// Async question/run events can arrive after the resize sent with a key.
		// Repaint while waiting so assertions do not depend on cell diffs.
		repaint := time.NewTicker(250 * time.Millisecond)
		defer repaint.Stop()
		var frames strings.Builder
		for {
			select {
			case frame := <-output.frames:
				frames.WriteString(ansi.Strip(frame))
				if strings.Contains(frames.String(), want) {
					return
				}
			case <-repaint.C:
				send("")
			case err := <-done:
				t.Fatalf("command stopped waiting for %q: %v\n%s", want, err, frames.String())
			case <-ctx.Done():
				t.Fatalf("missing %q\n%s", want, frames.String())
			}
		}
	}
	send("")
	waitFor("AICE")
	send("ask me\r")
	waitFor("Choose a mode")
	// Select an option, then replace it with a custom answer and switch
	// questions before submitting. This must not revive the original choice.
	send("12custom answer")
	waitFor("custom answer")
	send("\x1b[C")
	waitFor("Describe the goal")
	send("goal answer\r")
	waitFor("complete")
	send("/quit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("command did not quit")
	}
	<-stopped
	if len(backend.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(backend.requests))
	}
	request := backend.requests[1]
	result, ok := request.Messages[len(request.Messages)-1].(llm.ToolResultMessage)
	if !ok || result.IsError || result.ToolCallID != "question-1" {
		t.Fatalf("next request lost the question result: %#v", request.Messages)
	}
	var reply interaction.QuestionReply
	if err := json.Unmarshal([]byte(toolResultText(t, result)), &reply); err != nil {
		t.Fatal(err)
	}
	if answer := reply.Answers["mode"]; answer.SelectedOptionID != "" || answer.Text != "custom answer" {
		t.Fatalf("custom answer changed through CLI: %+v", answer)
	}
	if reply.Answers["goal"].Text != "goal answer" {
		t.Fatalf("goal answer changed through CLI: %+v", reply)
	}
}
