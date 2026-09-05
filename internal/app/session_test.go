package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestCloseInteractiveStoreRemovesEmptySession(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.jsonl")
	store, err := createSession(t.Context(), path, "empty-session", t.TempDir())
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if err := closeInteractiveStore(store); err != nil {
		t.Fatalf("closeInteractiveStore() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat() error = %v, want the empty session file removed", err)
	}
}

func TestCloseInteractiveStoreKeepsSessionWithMessages(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspacePath, sessionPath, "first prompt", "first answer")

	store, snapshot := openInteractiveCommandStore(
		t,
		workspacePath,
		sessionPath,
	)
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages before close = %d, want the recorded turn", len(snapshot.Messages))
	}
	if err := closeInteractiveStore(store); err != nil {
		t.Fatalf("closeInteractiveStore() error = %v", err)
	}
	kept, keptSnapshot := openInteractiveCommandStore(
		t,
		workspacePath,
		sessionPath,
	)
	defer func() {
		if err := kept.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	if len(keptSnapshot.Messages) != 2 {
		t.Fatalf("messages after close = %d, want the recorded turn kept", len(keptSnapshot.Messages))
	}
}

func TestCloseInteractiveStoreNilSafe(t *testing.T) {
	t.Parallel()

	if err := closeInteractiveStore(nil); err != nil {
		t.Fatalf("closeInteractiveStore(nil) error = %v", err)
	}
}

// appendTestSessionMessages builds fixture source entries; it is not a production
// persistence fallback and intentionally exercises the same per-message writer.
func appendTestSessionMessages(ctx context.Context, store *session.Store, messages []llm.AgentMessage) error {
	for _, message := range messages {
		if err := appendSessionMessage(ctx, store, message); err != nil {
			return err
		}
	}
	return nil
}

func sessionSourceMessages(snapshot session.Snapshot) []llm.AgentMessage {
	messages := make([]llm.AgentMessage, len(snapshot.Messages))
	for i, entry := range snapshot.Messages {
		messages[i] = entry.Message
	}
	return messages
}
