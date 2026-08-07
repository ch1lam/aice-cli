package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsProtectedResources(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "agent guidance")
	writeFile(t, filepath.Join(root, ".aice", "SYSTEM.md"), "base prompt")
	writeFile(t, filepath.Join(root, ".aice", "APPEND_SYSTEM.md"), "project notes")

	snapshot, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !snapshot.HasProtected() {
		t.Fatal("HasProtected() = false, want true")
	}
	found := make(map[string]bool, len(snapshot.Resources))
	for _, resource := range snapshot.Resources {
		found[resource.Name] = true
	}
	for _, name := range []string{
		AgentsFile,
		SystemFile,
		AppendSystemFile,
	} {
		if !found[name] {
			t.Errorf("Discover() missing resource %q", name)
		}
	}
}

func TestDiscoverIgnoresSessionsAndBareConfigDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".aice", "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(root, ".aice", "sessions", "abc.jsonl"), "{}")

	snapshot, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if snapshot.HasProtected() {
		t.Fatalf("HasProtected() = true, want false: %#v", snapshot.Resources)
	}
}

func TestDiscoverIgnoresNonRegularResource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".aice", "APPEND_SYSTEM.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	snapshot, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if snapshot.HasProtected() {
		t.Fatalf("HasProtected() = true, want false: %#v", snapshot.Resources)
	}
}

func TestDiscoverEmptyWorkspace(t *testing.T) {
	t.Parallel()

	snapshot, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if snapshot.HasProtected() {
		t.Fatalf("HasProtected() = true, want false")
	}
}

func TestDiscoverReportsInspectionError(t *testing.T) {
	t.Parallel()

	if _, err := Discover(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Discover() error = nil, want error")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
