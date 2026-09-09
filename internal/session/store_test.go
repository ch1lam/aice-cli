package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func mustCreate(t *testing.T, path string) *session.Store {
	t.Helper()
	store, err := session.Create(t.Context(), path, session.Metadata{
		ID: "session-1", CreatedAt: 100, WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func appendMessages(t *testing.T, store *session.Store, prefix string, messages ...llm.AgentMessage) []session.MessageEntry {
	t.Helper()
	var entries []session.MessageEntry
	for i, message := range messages {
		parent, err := store.LeafID()
		if err != nil {
			t.Fatal(err)
		}
		entry, err := session.NewMessage(fmt.Sprintf("%s-%d", prefix, i), parent, int64(i+100), message)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendMessage(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func snapshotOf(t *testing.T, store *session.Store) session.Snapshot {
	t.Helper()
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func appendBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}
func moveTo(t *testing.T, store *session.Store, id, target string) {
	t.Helper()
	parent, _ := store.LeafID()
	leaf, err := session.NewLeaf(id, parent, target, 200)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AppendLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreateAppendAndReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "session.jsonl")
	store := mustCreate(t, path)
	entries := appendMessages(t, store, "text", textMessages()...)
	entries = append(entries, appendMessages(t, store, "tool", toolMessages()...)...)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("mode: %o", info.Mode().Perm())
	}
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot := snapshotOf(t, reopened)
	if snapshot.Header.Version != 3 || !reflect.DeepEqual(snapshot.Messages, entries) {
		t.Fatalf("snapshot: %#v", snapshot)
	}
	user := snapshot.Messages[0].Message.(llm.UserMessage)
	user.Content[0].Text = "changed"
	if got := snapshotOf(t, reopened).Messages[0].Message.(llm.UserMessage).Content[0].Text; got != "hello" {
		t.Fatal("snapshot alias")
	}
	lines := strings.Split(strings.TrimSpace(string(fileBytes(t, path))), "\n")
	if len(lines) != 1+len(entries) {
		t.Fatalf("lines=%d", len(lines))
	}
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatal("invalid record")
		}
		if i > 0 {
			var record map[string]json.RawMessage
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}
			if record["message"] == nil || record["messages"] != nil || record["usage"] != nil {
				t.Fatal("not a single source message record")
			}
		}
	}
}

func TestStoreOpenTruncatesOnlyV3IncompleteTail(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	entries := appendMessages(t, store, "initial", textMessages()...)
	store.Close()
	before := fileBytes(t, path)
	appendBytes(t, path, []byte(`{"type":"message","created_at":`))
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(fileBytes(t, path), before) {
		t.Fatal("complete source changed")
	}
	if !reflect.DeepEqual(snapshotOf(t, reopened).Messages, entries) {
		t.Fatal("lost source")
	}
	appendMessages(t, reopened, "after", textMessages()...)
}

func TestStoreRejectsUnsupportedVersionsWithoutTouchingTail(t *testing.T) {
	t.Parallel()
	for _, version := range []int{1, 2, 4} {
		for _, tail := range []string{"", `{"type":"turn"`, "{bad-json}\n"} {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			data := []byte(fmt.Sprintf(`{"type":"session","version":%d,"id":"old","created_at":1,"working_directory":%q}`+"\n%s", version, t.TempDir(), tail))
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := session.Open(t.Context(), path); !errors.Is(err, session.ErrUnsupportedVersion) {
				t.Fatalf("version=%d err=%v", version, err)
			}
			if !reflect.DeepEqual(fileBytes(t, path), data) {
				t.Fatal("unsupported bytes modified")
			}
		}
	}
}

func TestStoreRejectsCorruptionWithoutChangingFile(t *testing.T) {
	t.Parallel()
	for _, tail := range []string{"{bad-json}\n", `{"type":"turn"}` + "\n"} {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		store := mustCreate(t, path)
		store.Close()
		appendBytes(t, path, []byte(tail))
		before := fileBytes(t, path)
		if _, err := session.Open(t.Context(), path); !errors.Is(err, session.ErrCorrupt) {
			t.Fatalf("err=%v", err)
		}
		if !reflect.DeepEqual(fileBytes(t, path), before) {
			t.Fatal("corrupt complete bytes modified")
		}
	}
}

func TestStoreRejectsInvalidSourceOrderWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{
		"orphan", "mismatched result", "duplicate result", "user interrupts",
		"assistant interrupts", "duplicate call", "duplicate record", "wrong parent",
	} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			store := mustCreate(t, path)
			messages := toolMessages()
			appendMessages(t, store, "user", messages[0])
			var bad llm.AgentMessage
			switch kind {
			case "orphan":
				bad = messages[2]
			case "duplicate call":
				a := messages[1].(llm.AssistantMessage)
				a.Content = append(a.Content, a.Content[len(a.Content)-1])
				bad = a
			default:
				appendMessages(t, store, "call", messages[1])
				switch kind {
				case "mismatched result":
					r := messages[2].(llm.ToolResultMessage)
					r.ToolName = "write"
					bad = r
				case "duplicate result":
					appendMessages(t, store, "result", messages[2])
					bad = messages[2]
				case "user interrupts":
					bad = messages[0]
				case "assistant interrupts":
					bad = messages[3]
				default:
					bad = messages[2]
				}
			}
			parent, _ := store.LeafID()
			id := "invalid"
			if kind == "duplicate record" {
				id = "user-0"
			}
			if kind == "wrong parent" {
				parent = "missing"
			}
			entry, err := session.NewMessage(id, parent, 300, bad)
			before := fileBytes(t, path)
			if err == nil {
				err = store.AppendMessage(t.Context(), entry)
			}
			if err == nil {
				t.Fatal("invalid prefix accepted")
			}
			if !reflect.DeepEqual(fileBytes(t, path), before) {
				t.Fatal("invalid append wrote bytes")
			}
		})
	}
}

func TestStoreRecoveryOnlyCompletesMissingResults(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	messages := toolMessages()
	a := messages[1].(llm.AssistantMessage)
	second := *a.Content[len(a.Content)-1].ToolCall
	second.ID = "call-2"
	a.Content = append(a.Content, llm.ContentPart{Type: llm.ContentTypeToolCall, ToolCall: &second})
	entries := appendMessages(t, store, "initial", messages[0], a, messages[2])
	store.Close()
	before := fileBytes(t, path)
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(fileBytes(t, path), before) {
		t.Fatal("Open appended recovery")
	}
	snapshot := snapshotOf(t, reopened)
	if _, err := session.BuildContext(snapshot); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("BuildContext=%v", err)
	}
	if _, err := session.PrepareCompaction(snapshot, session.CompactionSettings{KeepRecentTokens: 1}); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("PrepareCompaction=%v", err)
	}
	if _, err := session.Nodes(snapshot); err != nil {
		t.Fatalf("read-only tree=%v", err)
	}
	if err := reopened.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot = snapshotOf(t, reopened)
	if len(snapshot.Messages) != 4 || !reflect.DeepEqual(snapshot.Messages[:3], entries) {
		t.Fatal("existing results replaced")
	}
	recovered := snapshot.Messages[3].Message.(llm.ToolResultMessage)
	if recovered.ToolCallID != "call-2" || !recovered.IsError || !strings.Contains(recovered.Content[0].Text, "may have produced effects") {
		t.Fatalf("recovery=%#v", recovered)
	}
	after := fileBytes(t, path)
	if err := reopened.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fileBytes(t, path), after) {
		t.Fatal("recovery not idempotent")
	}
	if _, err := session.BuildContext(snapshot); err != nil {
		t.Fatal(err)
	}
	// A later group can reuse the provider call ID.
	appendMessages(t, reopened, "next", messages[1], messages[2], messages[3])
}

func TestStoreBranchMovesPreserveSourceAndRejectPendingTargets(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	first := appendMessages(t, store, "first", textMessages()...)
	pending := appendMessages(t, store, "pending", toolMessages()[1])
	before := fileBytes(t, path)
	leaf, _ := session.NewLeaf("unsafe", pending[0].ID, pending[0].ID, 300)
	if err := store.AppendLeaf(t.Context(), leaf); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("same unsafe target=%v", err)
	}
	if !reflect.DeepEqual(fileBytes(t, path), before) {
		t.Fatal("unsafe move changed source")
	}
	moveTo(t, store, "abandon", first[1].ID)
	replacement := appendMessages(t, store, "replacement", textMessages()...)
	if err := store.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotOf(t, store)
	if len(snapshot.Messages) != 5 || snapshot.LeafID != replacement[1].ID {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	old, err := session.Branch(snapshot, pending[0].ID)
	if err != nil || len(old) != 3 {
		t.Fatalf("old branch=%v error=%v", old, err)
	}
	contextMessages, err := session.BuildContext(snapshot)
	if err != nil || len(contextMessages) != 4 {
		t.Fatalf("context=%v err=%v", contextMessages, err)
	}
	store.Close()
	reopened, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(snapshotOf(t, reopened), snapshot) {
		t.Fatal("branch changed on replay")
	}
}

func TestStoreCancellationClosedAndMissingLeaf(t *testing.T) {
	t.Parallel()
	store := mustCreate(t, filepath.Join(t.TempDir(), "session.jsonl"))
	entry, _ := session.NewMessage("m", "", 100, textMessages()[0])
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := session.Open(ctx, store.Path()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	newPath := filepath.Join(t.TempDir(), "cancelled.jsonl")
	if _, err := session.Create(ctx, newPath, session.Metadata{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled create changed filesystem: %v", err)
	}
	if err := store.AppendMessage(ctx, entry); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.RecoverInterrupted(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	leaf, _ := session.NewLeaf("move", "", "missing", 200)
	before := fileBytes(t, store.Path())
	if err := store.AppendLeaf(t.Context(), leaf); !errors.Is(err, session.ErrEntryNotFound) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fileBytes(t, store.Path()), before) {
		t.Fatal("missing leaf wrote bytes")
	}
	store.Close()
	store.Close()
	if err := store.AppendMessage(t.Context(), entry); !errors.Is(err, session.ErrClosed) {
		t.Fatal(err)
	}
	if err := store.RecoverInterrupted(t.Context()); !errors.Is(err, session.ErrClosed) {
		t.Fatal(err)
	}
}

func TestTruncationMetadataSurvivesSessionReplay(t *testing.T) {
	for _, kind := range []string{"legacy", "read", "grep"} {
		legacy := kind == "legacy"
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			store := mustCreate(t, path)
			messages := toolMessages()
			result := messages[2].(llm.ToolResultMessage)
			if !legacy {
				result.Truncation = llm.ToolTruncation{Reason: llm.TruncationByteLimit, OutputLines: 2, OutputBytes: 40000, NextOffset: 3}
			}
			if kind == "grep" {
				assistant := messages[1].(llm.AssistantMessage)
				for i := range assistant.Content {
					if assistant.Content[i].ToolCall != nil {
						assistant.Content[i].ToolCall.Name = "grep"
					}
				}
				messages[1] = assistant
				result.ToolName = "grep"
				result.Truncation = llm.ToolTruncation{Reason: llm.TruncationByteLimit, MatchLimitReached: 100, LinesTruncated: true, OutputLines: 33, OutputBytes: 50000}
			}
			messages[2] = result
			appendMessages(t, store, "read", messages...)
			raw := fileBytes(t, path)
			if strings.Contains(string(raw), `"truncation"`) == legacy {
				t.Fatalf("unexpected optional field: %s", raw)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := session.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			history, err := session.BuildContext(snapshotOf(t, reopened))
			if err != nil {
				t.Fatal(err)
			}
			got := history[2].(llm.ToolResultMessage)
			if got.Truncation != result.Truncation {
				t.Fatalf("replay lost metadata: %+v", got)
			}
		})
	}
}
