package app

import (
	"os"
	"path/filepath"
	"testing"
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

func TestCloseInteractiveStoreKeepsSessionWithTurns(t *testing.T) {
	t.Parallel()

	workspacePath := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "conversation.jsonl")
	runPrintTurn(t, workspacePath, sessionPath, "first prompt", "first answer")

	store, snapshot := openInteractiveCommandStore(
		t,
		workspacePath,
		sessionPath,
	)
	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns before close = %d, want the recorded turn", len(snapshot.Turns))
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
	if len(keptSnapshot.Turns) != 1 {
		t.Fatalf("turns after close = %d, want the recorded turn kept", len(keptSnapshot.Turns))
	}
}

func TestCloseInteractiveStoreNilSafe(t *testing.T) {
	t.Parallel()

	if err := closeInteractiveStore(nil); err != nil {
		t.Fatalf("closeInteractiveStore(nil) error = %v", err)
	}
}
