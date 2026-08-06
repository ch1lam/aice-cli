package trust

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalPathResolvesSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := CanonicalPath(link)
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if got != want {
		t.Errorf("CanonicalPath(link) = %q, want %q", got, want)
	}
}

func TestCanonicalPathCleansTrailingSeparators(t *testing.T) {
	t.Parallel()

	root, err := CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	got, err := CanonicalPath(root + string(filepath.Separator))
	if err != nil {
		t.Fatalf("CanonicalPath() error = %v", err)
	}
	if got != root {
		t.Errorf("CanonicalPath(trailing sep) = %q, want %q", got, root)
	}
}

func TestCanonicalPathRejectsBlank(t *testing.T) {
	t.Parallel()

	if _, err := CanonicalPath(""); err == nil {
		t.Error("CanonicalPath() error = nil, want error")
	}
}

func TestParentPath(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		filepath.Join("a", "b", "c"),
		filepath.Join("a", "b"),
		filepath.Join("/", "a", "b", "c"),
	} {
		got, ok := ParentPath(path)
		want := filepath.Dir(path)
		if !ok {
			t.Errorf("ParentPath(%q) ok = false, want true", path)
			continue
		}
		if got != want {
			t.Errorf("ParentPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestParentPathRootHasNoParent(t *testing.T) {
	t.Parallel()

	if _, ok := ParentPath(rootForTest(t)); ok {
		t.Error("ParentPath(root) ok = true, want false")
	}
}

func rootForTest(t *testing.T) string {
	t.Helper()
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(t.TempDir())
		root = volume + string(filepath.Separator)
	}
	return root
}
