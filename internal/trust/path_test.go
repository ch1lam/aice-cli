package trust

import (
	"path/filepath"
	"runtime"
	"testing"
)

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
