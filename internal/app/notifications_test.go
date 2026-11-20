package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

// awaitSubagents waits while a subagent runs and returns its notification;
// with no subagent running it returns at once, even while another task such
// as a server keeps running, and cancellation ends the wait.
func TestAwaitSubagentsWaitsOnlyForSubagents(t *testing.T) {
	t.Parallel()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	await := awaitSubagents(tasks)

	if _, ok, err := await(t.Context()); ok || err != nil {
		t.Fatalf("await with nothing running = %v, %v", ok, err)
	}
	if _, err := tasks.StartRun(tool.TaskBash, "server", func(ctx context.Context, _ string, _ io.Writer) (*llm.ToolDelegation, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := await(t.Context()); ok || err != nil {
		t.Fatalf("await with only a command running = %v, %v", ok, err)
	}

	release := make(chan struct{})
	if _, err := tasks.StartRun(tool.TaskAgent, "probe", func(_ context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
		<-release
		_, err := io.WriteString(out, "found it")
		return &llm.ToolDelegation{SessionID: "child"}, err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, ok, err := await(ctx); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled await = %v, %v", ok, err)
	}
	close(release)
	input, ok, err := await(t.Context())
	if err != nil || !ok || !input.Message.IsNotice() || !strings.HasPrefix(input.ID, "notice-bg-") ||
		!strings.Contains(input.Message.Content[0].Text, "found it") || len(input.Message.Notice.Delegations) != 1 {
		t.Fatalf("await = %#v, %v, %v", input, ok, err)
	}
}

// An idle Session wakes for a finished task: AwaitCompletion returns, and a
// notification run answers the notification as its prompt, shows its summary
// and records it as AICE's. A second run finds nothing and does nothing.
func TestInteractiveNotificationRunAnswersFinishedTasks(t *testing.T) {
	t.Parallel()
	tasks, err := tool.NewTasks()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	model := &recordingModel{response: "the probe found it"}
	loop, err := agent.NewLoop(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := createAppTestSession(t, filepath.Join(t.TempDir(), "session.jsonl"), t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	runner := &interactiveSession{loop: loop, conversation: conversationState{store: store}, model: deepseek.DefaultModel(), tasks: tasks}

	waited := make(chan error, 1)
	go func() { waited <- runner.AwaitCompletion(t.Context()) }()
	if _, err := tasks.StartRun(tool.TaskAgent, "probe", func(_ context.Context, _ string, out io.Writer) (*llm.ToolDelegation, error) {
		_, err := io.WriteString(out, "found it in loader.go")
		return &llm.ToolDelegation{SessionID: "child", Usage: llm.Usage{TotalTokens: 7}}, err
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err != nil {
		t.Fatal(err)
	}

	var displays []interaction.Event
	sink := func(_ context.Context, event interaction.Event) error {
		displays = append(displays, event)
		return nil
	}
	for range 2 {
		active, err := runner.NewRun(t.Context(), interaction.RunInput{Notification: true}, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := active.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(model.requests) != 1 {
		t.Fatalf("requests = %d, want one run to answer", len(model.requests))
	}
	prompt, ok := model.requests[0].Messages[len(model.requests[0].Messages)-1].(llm.UserMessage)
	if !ok || !prompt.IsNotice() || !strings.Contains(prompt.Content[0].Text, "found it in loader.go") {
		t.Fatalf("prompt = %#v", model.requests[0].Messages)
	}
	if len(displays) == 0 || displays[0].Kind != interaction.EventNotification ||
		!strings.HasPrefix(displays[0].Input.Text, "Subagent bg-") || !strings.HasSuffix(displays[0].Input.Text, "finished: probe") {
		t.Fatalf("first display = %#v", displays)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := sessionTranscript(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if entries := transcript.Entries; len(entries) != 2 || entries[0].Kind != interaction.TranscriptNotification ||
		entries[0].Text != displays[0].Input.Text {
		t.Fatalf("transcript = %#v", transcript.Entries)
	}
	if _, err := runner.NewRun(t.Context(), interaction.RunInput{Notification: true, Prompt: "x"}, sink); err == nil {
		t.Fatal("a notification run accepted user input")
	}
}

// A notice is AICE's text, never the user's: it does not title a Session or
// enter its search text, the tree and compaction label it as a notification.
func TestNoticesAreNotUserText(t *testing.T) {
	t.Parallel()
	notice := llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("[AICE notification] report").Part()},
		Notice: &llm.Notice{Summary: "Subagent bg-1 finished: probe"}}
	typed := llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent("fix the loader").Part()}}
	reply := llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.NewTextContent("done").Part()}}
	entries := []session.MessageEntry{{ID: "n", Message: notice}, {ID: "a", Message: reply}, {ID: "u", Message: typed}, {ID: "b", Message: reply}}

	if user, answer := firstExchange(entries); user != "fix the loader" || answer != "done" {
		t.Errorf("first exchange = %q, %q", user, answer)
	}
	if text := sessionMessageText(notice); text != "" {
		t.Errorf("search text = %q", text)
	}
	node := session.Node{Type: session.RecordTypeMessage, ID: "n"}
	if label := sessionNodeDescription(node, map[string]session.MessageEntry{"n": entries[0]}, nil); !strings.HasPrefix(label, "notification ") ||
		!strings.Contains(label, "probe") {
		t.Errorf("tree label = %q", label)
	}
	serialized, err := serializeCompactionMessages([]llm.AgentMessage{notice, typed})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(serialized, "[AICE notification, not from the user]\n") || !strings.Contains(serialized, "[User]\nfix the loader") {
		t.Errorf("compaction input = %q", serialized)
	}
}
