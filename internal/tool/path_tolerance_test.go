package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNFDVariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "NFC e-acute", input: "caf\u00e9.txt", want: "cafe\u0301.txt"},
		{name: "NFC u-umlaut", input: "Z\u00fcrich", want: "Zu\u0308rich"},
		{name: "latin extended-a caron", input: "\u010d\u00e1s.txt", want: "c\u030Ca\u0301s.txt"},
		{name: "already decomposed", input: "cafe\u0301.txt", want: "cafe\u0301.txt"},
		{name: "Greek", input: "ά.txt", want: "α\u0301.txt"},
		{name: "Hangul", input: "각.txt", want: "\u1100\u1161\u11a8.txt"},
		{name: "recursive decomposition", input: "ậ.txt", want: "a\u0323\u0302.txt"},
		{name: "reorder combining marks", input: "a\u0301\u0323.txt", want: "a\u0323\u0301.txt"},
		{name: "equal combining classes keep order", input: "a\u0301\u0300.txt", want: "a\u0301\u0300.txt"},
		{name: "compatibility forms stay distinct", input: "ﬁ①Ａ.txt", want: "ﬁ①Ａ.txt"},
		{name: "no accents", input: "plain.txt", want: "plain.txt"},
		{name: "non-latin unchanged", input: "\u4f60\u597d.txt", want: "\u4f60\u597d.txt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := nfdVariant(test.input)
			if got != test.want {
				t.Fatalf("nfdVariant() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMacOSScreenshotVariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "am", input: "Shot at 3.42.10 AM.png", want: "Shot at 3.42.10\u202FAM.png"},
		{name: "pm", input: "Shot at 3.42.10 PM.png", want: "Shot at 3.42.10\u202FPM.png"},
		{name: "lowercase", input: "shot at 12.00 am.png", want: "shot at 12.00\u202Fam.png"},
		{name: "no pattern", input: "notes.txt", want: "notes.txt"},
		{name: "unmatched am", input: "ham.txt", want: "ham.txt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := macOSScreenshotVariant(test.input)
			if got != test.want {
				t.Fatalf("macOSScreenshotVariant() = %q, want %q", got, test.want)
			}
		})
	}
}

func newReadForTest(t *testing.T, root string) (*Read, *Workspace) {
	t.Helper()
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	read, err := NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	return read, workspace
}

func TestResolveReadTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	read, workspace := newReadForTest(t, root)
	for _, name := range []string{
		"cafe\u0301.txt", "it\u2019s.txt", "Capture d\u2019e\u0301cran.png",
		"Shot at 3.42.10\u202FAM.png", "foo bar.txt", "notes.md", filepath.Join("dir", "file.go"),
	} {
		writeToleranceFixture(t, filepath.Join(root, name))
	}
	writeToleranceFixture(t, filepath.Join(home, "a", "b.md"))

	tests := []struct {
		name, input string
		base, want  string // want is relative to base; empty base means the workspace
		wantErr     string
	}{
		{name: "NFD stored name", input: "caf\u00e9.txt", want: "cafe\u0301.txt"},
		{name: "curly apostrophe", input: "it's.txt", want: "it\u2019s.txt"},
		{name: "curly apostrophe and NFD", input: "Capture d'\u00e9cran.png", want: "Capture d\u2019e\u0301cran.png"},
		{name: "macOS screenshot AM", input: "Shot at 3.42.10 AM.png", want: "Shot at 3.42.10\u202FAM.png"},
		{name: "Unicode space input", input: "foo\u00A0bar.txt", want: "foo bar.txt"},
		{name: "at prefix", input: "@notes.md", want: "notes.md"},
		{name: "at prefix with dot path", input: "@./dir/file.go", want: filepath.Join("dir", "file.go")},
		{name: "tilde path", input: "~/a/b.md", base: home, want: filepath.Join("a", "b.md")},
		{name: "bare tilde", input: "~", base: home, want: "."},
		{name: "missing keeps base", input: "missing.txt", want: "missing.txt"},
		{name: "empty after prefix", input: "@", wantErr: "path is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := read.resolveReadTarget(test.input)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("resolveReadTarget() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveReadTarget() error = %v", err)
			}
			base := test.base
			if base == "" {
				base = workspace.PhysicalPath()
			}
			want := filepath.Join(base, test.want)
			// Normalization-insensitive filesystems (APFS/HFS+) also open the
			// typed spelling, so compare identity when the target exists.
			wantInfo, wantErr := os.Stat(want)
			gotInfo, gotErr := os.Stat(got)
			if wantErr != nil {
				if got != want {
					t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
				}
			} else if gotErr != nil || !os.SameFile(gotInfo, wantInfo) {
				t.Fatalf("resolveReadTarget() = %q does not name %q", got, want)
			}
		})
	}
}

func writeToleranceFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadUnicodeSpaceSet(t *testing.T) {
	t.Parallel()
	for _, space := range "\u00a0\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u202f\u205f\u3000" {
		t.Run(fmt.Sprintf("U+%04X", space), func(t *testing.T) {
			t.Parallel()
			if got := foldUnicodeSpaces("a" + string(space) + "b"); got != "a b" {
				t.Fatalf("foldUnicodeSpaces() = %q", got)
			}
		})
	}
	const unchanged = " \t\r\n\u0085\u1680\u200b\u2028\u2029\u2060\ufeff "
	if got := foldUnicodeSpaces(unchanged); got != unchanged {
		t.Fatalf("unrelated whitespace changed: %q", got)
	}
}

func TestReadVariantPriority(t *testing.T) {
	t.Parallel()
	// A byte-exact fake filesystem prevents APFS/HFS+ from making the base
	// path appear to exist merely because its decomposed variant exists.
	ordered := []string{
		"d'ậ at 1 AM.txt",
		"d'ậ at 1\u202fAM.txt",
		"d'a\u0323\u0302 at 1 AM.txt",
		"d’ậ at 1 AM.txt",
		"d’a\u0323\u0302 at 1 AM.txt",
	}
	for first, want := range ordered {
		t.Run(fmt.Sprintf("priority_%d", first), func(t *testing.T) {
			t.Parallel()
			existing := make(map[string]bool)
			for _, candidate := range ordered[first:] {
				existing[candidate] = true
			}
			got := findExistingVariant(ordered[0], func(path string) bool { return existing[path] })
			if got != want {
				t.Fatalf("selected %q, want %q", got, want)
			}
		})
	}
	if got := findExistingVariant(ordered[0], func(string) bool { return false }); got != ordered[0] {
		t.Fatalf("missing target changed to %q", got)
	}
}

func TestReadFoldedSpaceWinsConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a b.txt"), []byte("normalized"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a\u3000b.txt"), []byte("literal"), 0600); err != nil {
		t.Fatal(err)
	}
	read, _ := newReadForTest(t, root)
	content, err := read.Content(t.Context(), ReadRequest{Path: "@a\u3000b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 1 || content[0].Text != "normalized" {
		t.Fatalf("content = %#v", content)
	}
}

func TestReadDoesNotFallBackAfterSelectingDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "it's.txt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "it’s.txt"), []byte("wrong file"), 0600); err != nil {
		t.Fatal(err)
	}
	read, _ := newReadForTest(t, root)
	// A limit is invalid for a directory, so the selected base must fail
	// rather than retrying the curly-apostrophe file.
	if _, err := read.Content(t.Context(), ReadRequest{Path: "it's.txt", Limit: 1}); err == nil {
		t.Fatal("selected directory should reject pagination")
	}
}

func TestWorkspaceDoesNotApplyReadTolerance(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, workspace := newReadForTest(t, root)
	for _, input := range []string{"@file.txt", "~/file.txt", "a\u3000b.txt", "ậ.txt", "it's.txt", "1 AM.txt"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			got, err := workspace.resolvePath(input)
			if err != nil {
				t.Fatal(err)
			}
			// Windows accepts both separators; compare spelling without requiring
			// resolvePath to rewrite the input's forward slashes.
			if want := filepath.Join(workspace.PhysicalPath(), input); filepath.FromSlash(got) != want {
				t.Fatalf("mutation path = %q, want %q", got, want)
			}
		})
	}
}
