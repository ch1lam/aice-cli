package app

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/session"
)

func TestRenameSessionSameFileFallbackAndIsolation(t *testing.T) {
	s := browserHarness(t)
	current := browserFixture(t, s, "current", "Current question", "Current answer", 100)
	s.conversation.store = current
	other := browserFixture(t, s, "other", "Original question", "Original answer", 200)
	before, _ := os.ReadFile(other.Path())
	original, _ := other.Snapshot()
	if _, err := s.RenameSession(t.Context(), "other", "New title"); !errors.Is(err, session.ErrBusy) {
		t.Fatal("busy writer renamed", err)
	}
	other.Close()
	if _, err := s.SearchSessions(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	item, err := s.RenameSession(t.Context(), "other", "  新标题\n任务 ")
	if err != nil || item.Title != "新标题 任务" {
		t.Fatal(item, err)
	}
	after, _ := os.ReadFile(other.Path())
	if !bytes.HasPrefix(after, before) {
		t.Fatal("rename rewrote transcript")
	}
	snapshot, _, err := session.Read(t.Context(), other.Path())
	if err != nil || snapshot.LeafID != original.LeafID || len(snapshot.Messages) != len(original.Messages) || len(snapshot.Titles) != 1 {
		t.Fatal("rename changed history", err)
	}
	if s.conversation.store != current || s.transcript != nil {
		t.Fatal("rename switched current session")
	}
	found, err := s.SearchSessions(t.Context(), "新标题")
	if err != nil || len(found) != 1 || !found[0].TitleMatch {
		t.Fatal("title search retained stale cache", found, err)
	}
	item, err = s.RenameSession(t.Context(), "other", "")
	if err != nil || item.Title != "Original question" {
		t.Fatal("default title not restored", item, err)
	}
	if _, err := s.RenameSession(t.Context(), "current", "Current renamed"); err != nil {
		t.Fatal("active writer not reused", err)
	}
	currentSnapshot, _ := current.Snapshot()
	if len(currentSnapshot.Titles) != 1 || currentSnapshot.Titles[0].Title != "Current renamed" {
		t.Fatal("current metadata not retained")
	}
	files, _ := os.ReadDir(s.sessionDirectory())
	if len(files) != 2 {
		t.Fatal("extra metadata file")
	}
	preview, err := s.PreviewSession(t.Context(), "other", "")
	if err != nil || !strings.Contains(preview, "Original answer") {
		t.Fatal(preview, err)
	}
}
