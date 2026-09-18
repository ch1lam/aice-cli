package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
	"github.com/ch1lam/aice-cli/internal/tool"
)

func browserHarness(t testing.TB) *interactiveSession {
	t.Helper()
	workspace, err := tool.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &interactiveSession{workspace: workspace, workspacePath: workspace.PhysicalPath()}
}

func TestSessionScanPublishesRecentPrefixAndCancels(t *testing.T) {
	s := browserHarness(t)
	for i := range 20 {
		store := browserFixture(t, s, fmt.Sprintf("task%02d", i), "Question", "Answer", int64(i+1))
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(store.Path(), at, at); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var prefix []interaction.SessionSummary
	_, err := s.ScanSessions(ctx, "", func(items []interaction.SessionSummary) error {
		prefix = items
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || len(prefix) != 8 || prefix[0].Key != "task19" {
		t.Fatalf("prefix=%v, error=%v", prefix, err)
	}
	items, err := s.SearchSessions(t.Context(), "")
	if err != nil || len(items) != 20 || items[19].Key != "task00" {
		t.Fatal(items, err)
	}
	if len(prefix) != 8 || prefix[0].Key != "task19" {
		t.Fatal("published prefix mutated")
	}
}

func browserFixture(t testing.TB, s *interactiveSession, id, prompt, answer string, at int64) *session.Store {
	t.Helper()
	store, err := session.Create(t.Context(), filepath.Join(s.sessionDirectory(), id+".jsonl"), session.Metadata{
		ID: id, CreatedAt: at, WorkingDirectory: s.workspace.Path(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if prompt == "" {
		return store
	}
	for i, message := range []llm.AgentMessage{
		llm.UserMessage{Role: llm.RoleUser, Content: []llm.ContentPart{llm.NewTextContent(prompt).Part()}, Timestamp: at},
		llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.NewTextContent(answer).Part()},
			API: "test-api", Provider: "test-provider", ModelID: "test-model", StopReason: llm.StopReasonStop, Timestamp: at,
			Usage: llm.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}},
	} {
		parent, _ := store.LeafID()
		entry, err := session.NewMessage(id+string(rune('a'+i)), parent, at+int64(i), message)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendMessage(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestSessionBrowserSearchPreviewAndIsolation(t *testing.T) {
	t.Parallel()
	s := browserHarness(t)
	if items, err := s.SearchSessions(t.Context(), ""); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	old := browserFixture(t, s, "old", "中文会话", "这里提到 Unicorn 和恢复", 100)
	newer := browserFixture(t, s, "newer", "Current topic", "Recent answer", 200)
	browserFixture(t, s, "empty", "", "", 300)
	if err := os.WriteFile(filepath.Join(s.sessionDirectory(), "broken.jsonl"), []byte("bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := s.SearchSessions(t.Context(), "")
	if err != nil || len(items) != 3 {
		t.Fatalf("list = %#v: %v", items, err)
	}
	if items[0].Problem == "" || items[1].ID != "newer" || items[2].ID != "old" {
		t.Fatalf("sort/diagnostics: %#v", items)
	}
	before, _ := os.ReadFile(old.Path())
	items, err = s.SearchSessions(t.Context(), "UNICORN")
	if err != nil || len(items) != 1 || items[0].ID != "old" || !strings.Contains(items[0].Snippet, "Unicorn") {
		t.Fatal(items, err)
	}
	preview, err := s.PreviewSession(t.Context(), "old", "恢复")
	if err != nil || !strings.Contains(preview, "这里提到") {
		t.Fatal(preview, err)
	}
	after, _ := os.ReadFile(old.Path())
	if !bytes.Equal(before, after) {
		t.Fatal("search/preview changed bytes")
	}
	for _, key := range []string{"../old", "..\\old", "missing"} {
		if _, err := s.PreviewSession(t.Context(), key, ""); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.SearchSessions(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	newer.Close()
}

func TestSessionResumePreservesCurrentOnFailureAndRestoresSources(t *testing.T) {
	t.Parallel()
	s := browserHarness(t)
	old := browserFixture(t, s, "old", "Keep this", "Old answer", 100)
	target := browserFixture(t, s, "target", "Original before compaction", "Target answer", 200)
	s.conversation.store = old
	if err := s.conversation.reloadHistory(); err != nil {
		t.Fatal(err)
	}
	// A busy target is readable but cannot be resumed while its writer lives.
	if _, err := s.slashResume(t.Context(), interaction.CommandRequest{Arguments: "target"}); !errors.Is(err, session.ErrBusy) {
		t.Fatal(err)
	}
	if s.conversation.store != old {
		t.Fatal("failed switch detached old store")
	}
	snapshot, _ := target.Snapshot()
	checkpoint, err := session.NewCompaction(session.CompactionInput{ID: "compact", ParentID: snapshot.LeafID,
		CreatedAt: 300, Summary: "Only a summary", TokensBefore: 20, ActiveMessageCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := target.AppendCompaction(t.Context(), checkpoint); err != nil {
		t.Fatal(err)
	}
	target.Close()
	s.sideThreads = map[uint64]*sideThread{1: {id: 1}}
	s.conversation.activeMainRun = &mainRunState{}
	if _, err := s.slashResume(t.Context(), interaction.CommandRequest{Arguments: "target"}); err == nil {
		t.Fatal("switched during run")
	}
	s.conversation.activeMainRun = nil
	s.sideRunning = 1
	if _, err := s.slashResume(t.Context(), interaction.CommandRequest{Arguments: "target"}); err == nil {
		t.Fatal("switched during side run")
	}
	s.sideRunning = 0
	if _, err := s.slashResume(t.Context(), interaction.CommandRequest{Arguments: "target"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.conversation.store.Close() })
	if len(s.sideThreads) != 0 {
		t.Fatal("old side threads retained")
	}
	state := s.RuntimeState()
	if state.SessionID != "target" || state.Transcript == nil || !state.Transcript.ResetSideThreads {
		t.Fatalf("state: %#v", state)
	}
	if len(state.Transcript.Entries) != 2 || state.Transcript.Entries[0].Text != "Original before compaction" {
		t.Fatal("display used compacted context")
	}
	if _, ok := s.conversation.history[0].(llm.CompactionSummaryMessage); !ok {
		t.Fatal("model did not use summary")
	}
	if s.RuntimeState().Transcript != nil {
		t.Fatal("history transferred twice")
	}
	user, _ := llm.NewUserMessage(llm.NewTextContent("Continue here").Part())
	if err := appendSessionMessage(t.Context(), s.conversation.store, user); err != nil {
		t.Fatal(err)
	}
	if err := s.conversation.reloadHistory(); err != nil {
		t.Fatal(err)
	}
	before, _, err := session.Read(t.Context(), old.Path())
	if err != nil || len(before.Messages) != 2 {
		t.Fatal("old source changed", err)
	}
	current, _ := s.conversation.store.Snapshot()
	if current.Messages[2].ParentID != "compact" {
		t.Fatal("continued on wrong leaf")
	}
}
