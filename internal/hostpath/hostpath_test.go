package hostpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWithin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, "foo")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	child := filepath.Join(parent, "bar")
	sibling := filepath.Join(root, "foobar")

	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{name: "equal", parent: parent, child: parent, want: true},
		{name: "descendant", parent: parent, child: child, want: true},
		{name: "prefix sibling", parent: parent, child: sibling, want: false},
		{name: "parent empty", parent: "", child: child, want: false},
		{name: "child empty", parent: parent, child: "", want: false},
		{name: "outside", parent: parent, child: t.TempDir(), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Within(tt.parent, tt.child); got != tt.want {
				t.Fatalf("Within(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}

func TestSame(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if !Same(dir, dir) {
		t.Fatalf("Same(%q, %q) = false, want true", dir, dir)
	}
	cleaned := filepath.Clean(dir + string(filepath.Separator))
	if !Same(dir, cleaned) {
		t.Fatalf("Same after Clean = false, want true")
	}
	if Same(dir, t.TempDir()) {
		t.Fatal("Same distinct temp dirs = true, want false")
	}
	if runtime.GOOS != "windows" {
		if Same("/Users/Foo", "/Users/foo") {
			t.Fatal("unix Same should be case-sensitive")
		}
	}
}

func TestUnderHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}

	rel, ok := UnderHome(home)
	if !ok || rel != "." {
		t.Fatalf("UnderHome(home) = %q, %v, want ., true", rel, ok)
	}

	child := filepath.Join(home, "aice-hostpath-child")
	rel, ok = UnderHome(child)
	if !ok {
		t.Fatal("UnderHome(home child) ok = false")
	}
	if filepath.ToSlash(rel) != "aice-hostpath-child" {
		t.Fatalf("UnderHome child rel = %q, want aice-hostpath-child", rel)
	}

	outside := t.TempDir()
	if Within(home, outside) {
		t.Skip("temp dir is under home; cannot observe outside")
	}
	if _, ok := UnderHome(outside); ok {
		t.Fatalf("UnderHome(%q) ok = true, want false", outside)
	}
}

func TestExpandTilde(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}

	tests := []struct {
		name  string
		input string
	}{
		{name: "home", input: "~"},
		{name: "slash child", input: "~/foo"},
		{name: "backslash child", input: `~\foo`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExpandTilde(tt.input)
			want := filepath.Join(home, tt.input[1:])
			if got != want {
				t.Fatalf("ExpandTilde(%q) = %q, want %q", tt.input, got, want)
			}
		})
	}

	if got := ExpandTilde("plain"); got != "plain" {
		t.Fatalf("ExpandTilde(plain) = %q, want plain", got)
	}
	if got := ExpandTilde("~user"); got != "~user" {
		t.Fatalf("ExpandTilde(~user) = %q, want unchanged", got)
	}
}

func TestIsBashRooted(t *testing.T) {
	t.Parallel()

	if !IsBashRooted("/tmp/file") {
		t.Fatal("IsBashRooted(/tmp/file) = false, want true")
	}
	if IsBashRooted("tmp/file") {
		t.Fatal("IsBashRooted(tmp/file) = true, want false")
	}
	if IsBashRooted(`C:\Users\x`) {
		t.Fatal("IsBashRooted(drive path) = true, want false")
	}
}

func TestHomeDisplay(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}

	if got := HomeDisplay(home); got != "~" {
		t.Fatalf("HomeDisplay(home) = %q, want ~", got)
	}

	got := HomeDisplay(filepath.Join(home, "outside", "secret.env"))
	if got != "~/outside/secret.env" {
		t.Fatalf("HomeDisplay(nested) = %q, want ~/outside/secret.env", got)
	}
	if strings.Contains(got, `\`) {
		t.Fatalf("HomeDisplay uses backslash: %q", got)
	}

	outside := t.TempDir()
	if Within(home, outside) {
		t.Skip("temp dir is under home; cannot observe outside display")
	}
	displayed := HomeDisplay(outside)
	if strings.Contains(displayed, `\`) {
		t.Fatalf("HomeDisplay(outside) uses backslash: %q", displayed)
	}
	if filepath.ToSlash(filepath.Clean(outside)) != displayed {
		t.Fatalf("HomeDisplay(outside) = %q, want slashed clean path", displayed)
	}
}
