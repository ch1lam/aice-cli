//go:build windows

package hostpath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameCaseFold(t *testing.T) {
	t.Parallel()

	if !Same(`C:\Users\Foo`, `c:\users\foo`) {
		t.Fatal("Same case variants = false, want true")
	}
	if Same(`C:\Users\Foo`, `D:\Users\Foo`) {
		t.Fatal("Same cross-volume = true, want false")
	}
}

func TestWithinCaseFoldAndVolume(t *testing.T) {
	t.Parallel()

	if !Within(`C:\Users\Foo`, `c:\users\foo\secret.env`) {
		t.Fatal("Within case variants = false, want true")
	}
	if !Within(`C:\Users\Foo`, `C:\Users\Foo`) {
		t.Fatal("Within equal case variants = false, want true")
	}
	if Within(`C:\Users\Foo`, `D:\Users\Foo\secret.env`) {
		t.Fatal("Within cross-volume = true, want false")
	}
	if Within(`C:\Users\Foo`, `C:\Users\Foobar\x`) {
		t.Fatal("Within prefix sibling = true, want false")
	}
}

func TestUnderHomeCaseFold(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}
	flipped := flipASCIICase(home)
	child := filepath.Join(flipped, "aice-hostpath-case")
	rel, ok := UnderHome(child)
	if !ok {
		t.Fatalf("UnderHome(%q) ok = false", child)
	}
	if filepath.ToSlash(rel) != "aice-hostpath-case" {
		t.Fatalf("UnderHome case-fold rel = %q, want aice-hostpath-case", rel)
	}
}

func TestExpandTildeBackslashUsesJoin(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("user home directory is unavailable")
	}
	got := ExpandTilde(`~\foo\bar`)
	want := filepath.Join(home, `~\foo\bar`[1:])
	if got != want {
		t.Fatalf("ExpandTilde(~\\foo\\bar) = %q, want %q", got, want)
	}
	if !Within(home, got) {
		t.Fatalf("expanded path %q is not under home %q", got, home)
	}
}

func flipASCIICase(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + 32
		case c >= 'a' && c <= 'z':
			b[i] = c - 32
		}
	}
	return string(b)
}
