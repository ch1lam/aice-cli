package trust

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "trust.json"))
}

func TestNormalizeKey(t *testing.T) {
	t.Parallel()

	got := normalizeKey("/Canonical/Work")
	if runtime.GOOS == "windows" {
		if want := `\canonical\work`; got != want {
			t.Errorf("normalizeKey() = %q, want %q", got, want)
		}
	} else if got != "/Canonical/Work" {
		t.Errorf("normalizeKey() = %q, want /Canonical/Work (case preserved on Unix)", got)
	}
}

func TestStoreCaseInsensitiveLookupOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path case is insensitive")
	}

	store := newTestStore(t)
	if err := store.Set(`C:\Users\Foo\Project`, DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	entry, found, err := store.Lookup(`c:\users\foo\project`)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Decision != DecisionTrusted {
		t.Errorf("Lookup() = %#v, want trusted entry for differently-cased path", entry)
	}
}

func TestStoreLookupMissingStore(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, found, err := store.Lookup("/canonical/work"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if found {
		t.Fatal("Lookup() found = true, want false")
	}
}

func TestStoreSetAndLookupExact(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Set("/canonical/work", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	entry, found, err := store.Lookup("/canonical/work")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found {
		t.Fatal("Lookup() found = false, want true")
	}
	if entry.Path != filepath.Clean("/canonical/work") || entry.Decision != DecisionTrusted {
		t.Errorf("Lookup() = %#v, want trusted entry for /canonical/work", entry)
	}
}

func TestStoreLookupUsesNearestAncestor(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Set("/canonical", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	entry, found, err := store.Lookup("/canonical/work")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found {
		t.Fatal("Lookup() found = false, want true")
	}
	if entry.Path != filepath.Clean("/canonical") {
		t.Errorf("Lookup() path = %q, want /canonical", entry.Path)
	}
}

func TestStoreExactOverridesAncestor(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.SetMany([]Update{
		{Path: "/canonical", Decision: DecisionTrusted},
		{Path: "/canonical/work", Decision: DecisionUntrusted},
	}); err != nil {
		t.Fatalf("SetMany() error = %v", err)
	}
	entry, found, err := store.Lookup("/canonical/work")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Decision != DecisionUntrusted || entry.Path != filepath.Clean("/canonical/work") {
		t.Errorf("Lookup() = %#v, want untrusted exact entry", entry)
	}
}

func TestStoreSetUnknownRemovesEntry(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Set("/canonical/work", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Set("/canonical/work", DecisionUnknown); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, found, err := store.Lookup("/canonical/work"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if found {
		t.Fatal("Lookup() found = true after delete, want false")
	}
}

func TestStoreSetManyRemovesExactForParentChoice(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.Set("/canonical/work", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.SetMany([]Update{
		{Path: "/canonical", Decision: DecisionTrusted},
		{Path: "/canonical/work", Decision: DecisionUnknown},
	}); err != nil {
		t.Fatalf("SetMany() error = %v", err)
	}
	entry, found, err := store.Lookup("/canonical/work")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !found || entry.Path != filepath.Clean("/canonical") {
		t.Errorf("Lookup() = %#v, want inherited /canonical entry", entry)
	}
}

func TestStoreWritePermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix permission bits")
	}

	store := newTestStore(t)
	if err := store.Set("/canonical", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("store mode = %o, want 0600", mode)
	}
}

func TestStoreWritesSortedKeys(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := store.SetMany([]Update{
		{Path: "/zzz", Decision: DecisionTrusted},
		{Path: "/aaa", Decision: DecisionUntrusted},
	}); err != nil {
		t.Fatalf("SetMany() error = %v", err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	aaa := indexOf(t, text, filepath.Clean("/aaa"))
	zzz := indexOf(t, text, filepath.Clean("/zzz"))
	if aaa > zzz {
		t.Errorf("store keys not sorted: /aaa at %d, /zzz at %d", aaa, zzz)
	}
}

func TestStoreRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Lookup("/canonical"); err == nil {
		t.Fatal("Lookup() error = nil, want error")
	}
}

func TestStoreRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.WriteFile(store.Path(), []byte(
		`{"version":1,"projects":{},"surprise":true}`,
	), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Lookup("/canonical"); err == nil {
		t.Fatal("Lookup() error = nil, want error")
	}
}

func TestStoreRejectsNonBooleanDecision(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.WriteFile(store.Path(), []byte(
		`{"version":1,"projects":{"/canonical":null}}`,
	), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Lookup("/canonical"); err == nil {
		t.Fatal("Lookup() error = nil, want error")
	}
}

func TestStoreRejectsMultipleJSONValues(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.WriteFile(store.Path(), []byte(
		`{"version":1,"projects":{}}{"version":1}`,
	), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Lookup("/canonical"); err == nil {
		t.Fatal("Lookup() error = nil, want error")
	}
}

func TestStoreRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.WriteFile(store.Path(), []byte(
		`{"version":99,"projects":{}}`,
	), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Lookup("/canonical"); err == nil {
		t.Fatal("Lookup() error = nil, want error")
	}
}

func TestStoreConcurrentWritersKeepAllEntries(t *testing.T) {
	store := newTestStore(t)
	if err := store.Set("/canonical", DecisionTrusted); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	const writers = 8
	const entriesPerWriter = 5

	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for entry := 0; entry < entriesPerWriter; entry++ {
				path := filepath.Join("/canonical", "p", "w", strconv.Itoa(writer), strconv.Itoa(entry))
				if err := store.Set(path, DecisionTrusted); err != nil {
					t.Errorf("Set() error = %v", err)
					return
				}
			}
		}(writer)
	}
	wg.Wait()

	if _, found, err := store.Lookup("/canonical"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if !found {
		t.Fatal("Lookup(/canonical) not found; an update was lost")
	}
	// Reading back every writer's last entry proves no update was lost.
	for writer := 0; writer < writers; writer++ {
		path := filepath.Join("/canonical", "p", "w", strconv.Itoa(writer), strconv.Itoa(entriesPerWriter-1))
		if _, found, err := store.Lookup(path); err != nil {
			t.Fatalf("Lookup() error = %v", err)
		} else if !found {
			t.Errorf("Lookup(%q) not found; a concurrent update was lost", path)
		}
	}
}

func indexOf(t *testing.T, text, needle string) int {
	t.Helper()
	for index := 0; index+len(needle) <= len(text); index++ {
		if text[index:index+len(needle)] == needle {
			return index
		}
	}
	t.Fatalf("%q not found in %q", needle, text)
	return -1
}
