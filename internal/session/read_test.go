package session_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ch1lam/aice-cli/internal/session"
)

func TestReadDoesNotRepairOrRecover(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store := mustCreate(t, path)
	entries := appendMessages(t, store, "pending", toolMessages()[:2]...)
	appendBytes(t, path, []byte(`{"type":"message"`))
	before := fileBytes(t, path)
	snapshot, incomplete, err := session.Read(t.Context(), path)
	if err != nil || !incomplete || !reflect.DeepEqual(snapshot.Messages, entries) {
		t.Fatalf("Read = %#v, incomplete %v, error %v", snapshot, incomplete, err)
	}
	if !reflect.DeepEqual(fileBytes(t, path), before) {
		t.Fatal("browsing modified history")
	}
	if _, err := session.BuildContext(snapshot); !errors.Is(err, session.ErrIncompleteGroup) {
		t.Fatalf("read silently recovered tool calls: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := session.Read(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	store.Close()
	appendBytes(t, path, []byte("\n"))
	if _, _, err := session.Read(t.Context(), path); !errors.Is(err, session.ErrCorrupt) {
		t.Fatalf("bad complete line: %v", err)
	}
}

func TestSessionWriterExclusionAndReadAccess(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store := mustCreate(t, path)
	appendMessages(t, store, "text", textMessages()...)
	before := fileBytes(t, path)
	if other, err := session.Open(t.Context(), path); !errors.Is(err, session.ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second writer: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSessionWriterChild$")
	child.Env = append(os.Environ(), "AICE_TEST_LOCKED_SESSION="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %s: %v", output, err)
	}
	if !reflect.DeepEqual(before, fileBytes(t, path)) {
		t.Fatal("rejected writer changed bytes")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	other.Close()
}

func TestSessionWriterChild(t *testing.T) {
	path := os.Getenv("AICE_TEST_LOCKED_SESSION")
	if path == "" {
		t.Skip("subprocess only")
	}
	if other, err := session.Open(t.Context(), path); !errors.Is(err, session.ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("cross-process writer: %v", err)
	}
	if snapshot, _, err := session.Read(t.Context(), path); err != nil || len(snapshot.Messages) != 2 {
		t.Fatalf("read access denied: %v", err)
	}
}
