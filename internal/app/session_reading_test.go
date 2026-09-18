package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestSessionSearchTitlesFirstAndReadOtherBranch(t *testing.T) {
	s := browserHarness(t)
	store := browserFixture(t, s, "body", "Original question", "Abandoned needle answer", 200)
	leaf, _ := session.NewLeaf("move", "bodyb", "bodya", 300)
	if err := store.AppendLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	answer := llm.AssistantMessage{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.NewTextContent("Active answer").Part()},
		API: "test", Provider: "test", ModelID: "test", StopReason: llm.StopReasonStop, Timestamp: 301}
	entry, _ := session.NewMessage("active", "bodya", 301, answer)
	if err := store.AppendMessage(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	browserFixture(t, s, "title", "Needle task", "Older title match", 100)
	before, _ := os.ReadFile(store.Path())
	var batches [][]interaction.SessionSummary
	items, err := s.ScanSessions(t.Context(), "needle", func(items []interaction.SessionSummary) error { batches = append(batches, items); return nil })
	if err != nil || len(batches) == 0 || len(batches[0]) != 1 || batches[0][0].Key != "title" {
		t.Fatal(batches, err)
	}
	if len(items) != 2 || items[0].Key != "title" || items[1].MatchID != "bodyb" || !items[1].OtherBranch {
		t.Fatal(items)
	}
	view, err := s.ReadSession(t.Context(), "body", items[1].MatchID)
	if err != nil || !view.OtherBranch || view.Transcript.Entries[1].Assistant.Text != "Abandoned needle answer" || view.Active.Entries[1].Assistant.Text != "Active answer" {
		t.Fatal(view, err)
	}
	after, _ := os.ReadFile(store.Path())
	if !bytes.Equal(before, after) {
		t.Fatal("inspection modified source history")
	}
	current, _ := store.LeafID()
	if current != "active" {
		t.Fatal("inspection checked out a branch")
	}
	if _, err := s.ReadSession(t.Context(), "body", "missing"); err == nil {
		t.Fatal("missing match accepted")
	}
}
