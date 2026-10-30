package app

import (
	"encoding/json"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

// assertRequestsRebuildFromSession checks the evaluation contract that every
// assistant message's model input can be rebuilt from the reopened Session.
// requests are the main-loop calls in order; compaction summary calls are
// skipped because they produce checkpoints, not assistant messages.
func assertRequestsRebuildFromSession(t *testing.T, path string, requests []llm.Request) {
	t.Helper()
	snapshot := openSessionSnapshot(t, path)
	var sent []llm.Request
	for _, request := range requests {
		if request.SystemPrompt != compactionSystemPrompt {
			sent = append(sent, request)
		}
	}
	var assistants []session.MessageEntry
	for _, entry := range snapshot.Messages {
		if _, ok := entry.Message.(llm.AssistantMessage); ok {
			assistants = append(assistants, entry)
		}
	}
	if len(assistants) != len(sent) {
		t.Fatalf("assistant messages = %d, model requests = %d", len(assistants), len(sent))
	}
	for i, entry := range assistants {
		rebuilt, err := session.RebuildRequest(snapshot, entry.ID)
		if err != nil {
			t.Fatalf("RebuildRequest(%s) error = %v", entry.ID, err)
		}
		want, err := json.Marshal(sent[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(rebuilt)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("rebuilt request %d differs\n got: %s\nwant: %s", i, got, want)
		}
	}
}
