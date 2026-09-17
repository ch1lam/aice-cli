package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
)

func TestFailedWriteDoesNotConsumeCachedCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store, err := Create(t.Context(), path, Metadata{ID: "test", CreatedAt: 1, WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, _ := llm.NewUserMessage(llm.NewTextContent("inspect").Part())
	entry, _ := NewMessage("user", "", 1, user)
	if err := store.AppendMessage(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	assistant := llm.NewAssistantMessage(llm.Model{API: "test", Provider: "test", ID: "test"})
	assistant.StopReason = llm.StopReasonToolUse
	assistant.Content = []llm.ContentPart{{Type: llm.ContentTypeToolCall, ToolCall: &llm.ToolCall{ID: "call", Name: "read", Arguments: []byte(`{}`)}}}
	entry, _ = NewMessage("assistant", "user", 2, assistant)
	if err := store.AppendMessage(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	result, _ := llm.NewToolResultMessage(llm.ToolResult{CallID: "call", Name: "read", Content: []llm.ContentPart{llm.NewTextContent("result").Part()}})
	entry, _ = NewMessage("result", "assistant", 3, result)
	before, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// Stat succeeds, but writing through a read-only handle fails. Retrying the
	// same record through the real handle must still see its pending tool call.
	readonly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	writable := store.file
	store.file = readonly
	writeErr := store.AppendMessage(t.Context(), entry)
	store.file = writable
	readonly.Close()
	if writeErr == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	after, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed write published state")
	}
	if _, err := store.ContextSince("user"); !errors.Is(err, ErrIncompleteGroup) {
		t.Fatalf("pending call was consumed: %v", err)
	}
	if err := store.AppendMessage(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	update, err := store.ContextSince("user")
	if err != nil || len(update.Messages) != 2 {
		t.Fatalf("retry: %+v %v", update, err)
	}
}
