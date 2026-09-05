package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/provider/deepseek"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestInteractiveSessionPersistsToolResultAfterDisplayFailure(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	changedPath := filepath.Join(workspace, "changed.txt")
	call := llm.ToolCall{ID: "mutate-1", Name: "mutate", Arguments: []byte(`{}`)}
	tool := newAppTestTool("mutate", func(_ context.Context, call llm.ToolCall) (llm.ToolResult, error) {
		if err := os.WriteFile(changedPath, []byte("changed"), 0o600); err != nil {
			return llm.ToolResult{}, err
		}
		return llm.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: []llm.ContentPart{llm.NewTextContent("changed").Part()},
		}, nil
	})
	model := &toolLoopModel{firstCall: &call}
	store := createAppTestSession(t, path, workspace)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	runner := &interactiveSession{
		loop:         mustAppLoop(t, model, []agent.Tool{tool}),
		conversation: conversationState{store: store},
		model:        deepseek.DefaultModel(),
	}
	displayFailure := errors.New("display disconnected")
	err := runInteractive(t.Context(), runner, "make the change", func(_ context.Context, event interaction.Event) error {
		if event.Kind == interaction.EventToolEnd {
			return displayFailure
		}
		return nil
	})
	if !errors.Is(err, displayFailure) {
		t.Fatalf("Run() error = %v, want display failure", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want execution stopped after display failure", len(model.requests))
	}
	data, err := os.ReadFile(changedPath)
	if err != nil || string(data) != "changed" {
		t.Fatalf("side effect = %q, error = %v", data, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	history, err := sessionHistory(openSessionSnapshot(t, path))
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, message := range history {
		result, ok := message.(llm.ToolResultMessage)
		if !ok || result.ToolCallID != call.ID {
			continue
		}
		results++
		if result.IsError || len(result.Content) != 1 || result.Content[0].Text != "changed" {
			t.Fatalf("restored tool result = %#v, want actual successful result", result)
		}
	}
	if results != 1 {
		t.Fatalf("restored tool results = %d, want exactly one", results)
	}
	if !reflect.DeepEqual(runner.conversation.history, history) {
		t.Fatal("in-memory and restored history differ after display failure")
	}
}

func TestInteractiveSessionDoesNotDuplicateHistoryAfterFinalDisplayFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	model := &recordingModel{response: "answer"}
	store := createAppTestSession(t, path, t.TempDir())
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	runner := &interactiveSession{
		loop:         mustAppLoop(t, model, nil),
		conversation: conversationState{store: store},
		model:        deepseek.DefaultModel(),
	}
	displayFailure := errors.New("final display failed")
	err := runInteractive(t.Context(), runner, "question", func(_ context.Context, event interaction.Event) error {
		if event.Kind == interaction.EventAgentEnd {
			return displayFailure
		}
		return nil
	})
	if !errors.Is(err, displayFailure) {
		t.Fatalf("Run() error = %v, want final display failure", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	history, err := sessionHistory(openSessionSnapshot(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("restored messages = %d, want one question and one answer", len(history))
	}
	assertTextMessage(t, history[0], llm.RoleUser, "question")
	assertTextMessage(t, history[1], llm.RoleAssistant, "answer")
	if !reflect.DeepEqual(runner.conversation.history, history) {
		t.Fatal("in-memory and restored history differ after final display failure")
	}
}

func TestInteractiveSessionPersistenceFailureStopsFollowUp(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	model := &recordingModel{response: "answer"}
	store := createAppTestSession(t, path, t.TempDir())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	runner := &interactiveSession{
		loop:         mustAppLoop(t, model, nil),
		conversation: conversationState{store: store},
		model:        deepseek.DefaultModel(),
	}
	active, err := runner.NewRun(interaction.RunInput{Prompt: "question"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := active.Deliver(interaction.Delivery{
		ID: "next", Text: "continue", Kind: interaction.DeliveryKindFollowUp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := active.Run(t.Context()); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("Run() error = %v, want persistence failure", err)
	}
	if len(model.requests) > 1 {
		t.Fatalf("model requests = %d, follow-up ran after persistence failed", len(model.requests))
	}
	history, err := sessionHistory(openSessionSnapshot(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 || len(runner.conversation.history) != 0 {
		t.Fatalf("failed save published history: disk = %d, memory = %d", len(history), len(runner.conversation.history))
	}
	if runner.conversation.activeMainRun != nil {
		t.Fatal("failed save left the main run active")
	}
}
