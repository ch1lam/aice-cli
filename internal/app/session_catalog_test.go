package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ch1lam/aice-cli/internal/llm"
	"github.com/ch1lam/aice-cli/internal/session"
)

func TestSessionCatalogInvalidation(t *testing.T) {
	s := browserHarness(t)
	store := browserFixture(t, s, "one", "First question", "First answer", 100)
	first, err := s.catalogSession(t.Context(), "one")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := s.catalogSession(t.Context(), "one")
	if err != nil || first != cached {
		t.Fatal("unchanged file was replayed", err)
	}
	message, _ := llm.NewUserMessage(llm.NewTextContent("Latest question").Part())
	if err := appendSessionMessage(t.Context(), store, message); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewSession(t.Context(), "one", "")
	if err != nil || !strings.Contains(preview, "Latest question") || strings.Contains(preview, "First question") {
		t.Fatal("append did not invalidate latest-question preview", preview, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), "Latest question", "Newest question"))
	// Same size and mtime, different file identity.
	replacement := store.Path() + ".replacement"
	if err := os.WriteFile(replacement, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, store.Path()); err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewSession(t.Context(), "one", "")
	if err != nil || !strings.Contains(preview, "Newest question") {
		t.Fatal(preview, err)
	}
	if err := os.Remove(store.Path()); err != nil {
		t.Fatal(err)
	}
	items, err := s.SearchSessions(t.Context(), "")
	if err != nil || len(items) != 0 || len(s.catalog.entries) != 0 {
		t.Fatal("deleted file retained", items, err)
	}
}

func TestSessionCatalogCapacityAndConcurrentReaders(t *testing.T) {
	var cache sessionCatalog
	for i := range sessionCatalogEntries + 10 {
		cache.put(fmt.Sprint(i), &sessionCatalogEntry{bytes: 1024})
	}
	if len(cache.entries) != sessionCatalogEntries {
		t.Fatal("entry capacity not enforced")
	}
	cache.put("large", &sessionCatalogEntry{bytes: sessionCatalogBytes})
	if len(cache.entries) != 1 || cache.bytes != sessionCatalogBytes {
		t.Fatal("byte capacity not enforced")
	}
	cache.put("oversized", &sessionCatalogEntry{bytes: sessionCatalogBytes + 1})
	if len(cache.entries) != 1 {
		t.Fatal("oversized entry retained")
	}
	s := browserHarness(t)
	store := browserFixture(t, s, "one", "Question", "Answer", 100)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 5 {
				if _, err := s.SearchSessions(t.Context(), "answer"); err != nil {
					t.Error(err)
				}
				if _, err := s.PreviewSession(t.Context(), "one", ""); err != nil {
					t.Error(err)
				}
			}
		})
	}
	message, _ := llm.NewUserMessage(llm.NewTextContent("Concurrent append").Part())
	if err := appendSessionMessage(t.Context(), store, message); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestSessionExcerptUnicodeAndBoundaries(t *testing.T) {
	text := strings.Repeat("İ中文", 80) + "NEEDLE" + strings.Repeat("后", 100)
	got := sessionExcerpt(text, "needle", 60)
	if !strings.Contains(got, "NEEDLE") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatal(got)
	}
	if got := sessionExcerpt("中文abc", "", 2); got != "中文…" {
		t.Fatal(got)
	}
}

func TestSessionCatalogAccountsForLongSessionIdentity(t *testing.T) {
	s := browserHarness(t)
	id := strings.Repeat("session-id-", 10000)
	store, err := session.Create(t.Context(), filepath.Join(s.sessionDirectory(), "short-key.jsonl"), session.Metadata{
		ID: id, CreatedAt: 100, WorkingDirectory: s.workspace.Path(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	entry, err := s.catalogSession(t.Context(), "short-key")
	if err != nil {
		t.Fatal(err)
	}
	if entry.summary.ID != id || entry.bytes < len(id) {
		t.Fatal("retained identity exceeds accounted cache bytes")
	}
	// Leave less budget than that identity requires. Retaining it must evict
	// the earlier entry, even though its filename and title are both short.
	var cache sessionCatalog
	cache.put("older", &sessionCatalogEntry{bytes: sessionCatalogBytes - len(id)/2})
	cache.put("short-key", entry)
	if cache.entries["older"] != nil || cache.entries["short-key"] == nil || cache.bytes > sessionCatalogBytes {
		t.Fatal("identity bytes did not participate in eviction")
	}
}
