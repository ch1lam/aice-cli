package session_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/session"
)

func TestTitleAppendPreservesTranscriptAndReplaysLatest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	appendMessages(t, store, "text", textMessages()...)
	before := fileBytes(t, path)
	original, _ := store.Snapshot()
	contextBefore, _ := session.BuildContext(original)
	for i, name := range []string{"First title", "Second 中文 title", ""} {
		title, err := session.NewTitle(string(rune('a'+i)), name, int64(200+i))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendTitle(t.Context(), title); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _ := store.Snapshot()
	if len(snapshot.Titles) != 3 || snapshot.Titles[2].Title != "" || snapshot.LeafID != original.LeafID || !reflect.DeepEqual(snapshot.Order, original.Order) {
		t.Fatal("metadata changed conversation tree")
	}
	after := fileBytes(t, path)
	if !bytes.HasPrefix(after, before) || bytes.Count(after[len(before):], []byte("\n")) != 3 {
		t.Fatal("rename rewrote source bytes")
	}
	contextAfter, _ := session.BuildContext(snapshot)
	if !reflect.DeepEqual(contextBefore, contextAfter) {
		t.Fatal("title entered model context")
	}
	snapshot.Titles[0].Title = "mutated snapshot"
	store.Close()
	reopened, err := session.OpenComplete(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, _ := reopened.Snapshot()
	if loaded.Titles[0].Title != "First title" || loaded.Titles[2].Title != "" || loaded.LeafID != original.LeafID {
		t.Fatal("metadata replay changed state")
	}
	title, _ := session.NewTitle("a", "duplicate", 300)
	if err := reopened.AppendTitle(t.Context(), title); err == nil {
		t.Fatal("replayed metadata ID not reserved")
	}
	read, incomplete, err := session.Read(t.Context(), path)
	if err != nil || incomplete || !reflect.DeepEqual(read.Titles, loaded.Titles) {
		t.Fatal("read-only replay lost titles", err)
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatal("rename created additional files")
	}
}

func TestOpenCompleteRejectsTailAndBusyWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store := mustCreate(t, path)
	if other, err := session.OpenComplete(t.Context(), path); !errors.Is(err, session.ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("writer exclusion: %v", err)
	}
	store.Close()
	appendBytes(t, path, []byte(`{"type":"title","title":"unfinished`))
	before := fileBytes(t, path)
	if other, err := session.OpenComplete(t.Context(), path); !errors.Is(err, session.ErrIncompleteTail) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("tail accepted: %v", err)
	}
	if !bytes.Equal(before, fileBytes(t, path)) {
		t.Fatal("metadata open repaired history")
	}
	snapshot, incomplete, err := session.Read(t.Context(), path)
	if err != nil || !incomplete || len(snapshot.Titles) != 0 {
		t.Fatal("partial title applied", err)
	}
	repaired, err := session.Open(t.Context(), path)
	if err != nil {
		t.Fatal("explicit resume cannot repair", err)
	}
	repaired.Close()
}

func TestTitleValidationAndPendingTools(t *testing.T) {
	for _, name := range []string{strings.Repeat("长", 201), "bad\x1btitle", "bad\u202etitle", string([]byte{255})} {
		if _, err := session.NewTitle("title", name, 1); err == nil {
			t.Fatalf("accepted invalid title %q", name)
		}
	}
	title, err := session.NewTitle("title", "  good\n title  ", 1)
	if err != nil || title.Title != "good title" {
		t.Fatal(title, err)
	}
	store := mustCreate(t, filepath.Join(t.TempDir(), "pending.jsonl"))
	appendMessages(t, store, "pending", toolMessages()[:2]...)
	before, _ := store.LeafID()
	if err := store.AppendTitle(t.Context(), title); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := store.Snapshot()
	if _, err := session.BuildContext(snapshot); !errors.Is(err, session.ErrIncompleteGroup) || snapshot.LeafID != before {
		t.Fatal("title recovered pending tools", err)
	}
}
