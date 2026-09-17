package session_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestContextUpdatesMatchRebuiltHistory(t *testing.T) {
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	cursor := ""
	history := make([]llm.AgentMessage, 0)
	check := func(wantReplace bool) {
		t.Helper()
		update, err := store.ContextSince(cursor)
		if err != nil {
			t.Fatal(err)
		}
		if update.Replace != wantReplace {
			t.Fatalf("replacement=%v, want %v", update.Replace, wantReplace)
		}
		if update.Replace {
			history = update.Messages
		} else {
			history = append(history, update.Messages...)
		}
		cursor = update.LeafID
		expected, err := session.BuildContext(snapshotOf(t, store))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(history, expected) {
			t.Fatalf("incremental history differs at %s", cursor)
		}
	}
	check(false)
	first := appendMessages(t, store, "first", textMessages()...)
	check(false)
	for round := range 3 {
		messages := toolMessages()
		appendMessages(t, store, "calls-"+string(rune('a'+round)), messages[1])
		if _, err := store.ContextSince(cursor); !errors.Is(err, session.ErrIncompleteGroup) {
			t.Fatalf("pending update: %v", err)
		}
		appendMessages(t, store, "results-"+string(rune('a'+round)), messages[2], messages[3])
		check(false)
	}
	moveTo(t, store, "back", first[1].ID)
	appendMessages(t, store, "sibling", textMessages()...)
	check(true)
	checkpoint := mustCompaction(t, session.CompactionInput{ID: "compact", ParentID: cursor, CreatedAt: 300, Summary: "first summarized", TokensBefore: 100, FirstKeptMessageID: "sibling-0", ActiveMessageCount: 4, RetainedMessageCount: 2})
	if err := store.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	check(true)
	appendMessages(t, store, "after-compaction", textMessages()...)
	check(false)
	path := store.Path()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	store, err = session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	check(false)
	appendMessages(t, store, "reopened", textMessages()...)
	check(false)
	moveTo(t, store, "root", "")
	check(true)
	unknown, err := store.ContextSince("unknown")
	if err != nil || !unknown.Replace || len(unknown.Messages) != 0 {
		t.Fatalf("unknown cursor: %+v %v", unknown, err)
	}
}

func TestContextUpdatesRetainPendingPrefixesAndCopyPayloads(t *testing.T) {
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	messages := toolMessages()
	entries := appendMessages(t, store, "tool", messages[:3]...)
	// Consuming the result at the leaf must not consume the call in its parent.
	if _, err := store.ContextSince(entries[1].ID); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("accepted incomplete cursor: %v", err)
	}
	leaf, _ := session.NewLeaf("bad-move", entries[2].ID, entries[1].ID, 300)
	if err := store.AppendLeaf(t.Context(), leaf); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("accepted incomplete checkout: %v", err)
	}
	update, err := store.ContextSince(entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(update.Messages) != 2 || update.Replace {
		t.Fatalf("expected one complete tool group: %+v", update)
	}
	assistant := update.Messages[0].(llm.AssistantMessage)
	assistant.Content[len(assistant.Content)-1].ToolCall.Arguments[0] = '!'
	assistant.Usage.Cost.Total = 99
	update.Messages[1].(llm.ToolResultMessage).Content[0].Text = "mutated"
	fresh, err := store.ContextSince(entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh.Messages, messages[1:3]) {
		t.Fatal("caller mutated stored tool group")
	}
}

func TestNewMessageMatchesDurableJSONRepresentation(t *testing.T) {
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	messages := toolMessages()
	messages[0].(llm.UserMessage).Content[0].Text = "invalid utf8: \xff"
	assistant := messages[1].(llm.AssistantMessage)
	assistant.Content[len(assistant.Content)-1].ToolCall.Arguments = []byte("{ \"path\" : \"a<b\" }")
	entries := appendMessages(t, store, "canonical", messages[:3]...)
	if !reflect.DeepEqual(snapshotOf(t, store).Messages, entries) {
		t.Fatal("new entry differs from durable representation")
	}
}

func TestContextUpdatePairsDurableToolIdentities(t *testing.T) {
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	messages := toolMessages()
	entries := appendMessages(t, store, "user", messages[0])
	assistant := messages[1].(llm.AssistantMessage)
	call := assistant.Content[len(assistant.Content)-1].ToolCall
	call.ID = "call-\xff"
	// Direct entries also acquire JSON's canonical representation. Cached
	// pairing must agree with replay even if NewMessage was not used.
	entry := session.MessageEntry{
		Type: session.RecordTypeMessage, ID: "assistant", ParentID: entries[0].ID,
		CreatedAt: 1, Message: assistant,
	}
	if err := store.AppendMessage(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	result := messages[2].(llm.ToolResultMessage)
	result.ToolCallID = "call-\ufffd"
	appendMessages(t, store, "result", result)
	update, err := store.ContextSince(entries[0].ID)
	if err != nil || len(update.Messages) != 2 {
		t.Fatalf("canonical pairing: %+v %v", update, err)
	}
}
