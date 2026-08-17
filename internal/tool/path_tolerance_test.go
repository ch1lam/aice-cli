package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeReadInputStripsAtPrefix(t *testing.T) {
	t.Parallel()
	if got, want := normalizeReadInput("@notes.md"), "notes.md"; got != want {
		t.Fatalf("normalizeReadInput() = %q, want %q", got, want)
	}
	if got, want := normalizeReadInput("@./dir/file.go"), "./dir/file.go"; got != want {
		t.Fatalf("normalizeReadInput() = %q, want %q", got, want)
	}
	if got, want := normalizeReadInput("notes.md"), "notes.md"; got != want {
		t.Fatalf("normalizeReadInput() = %q, want %q", got, want)
	}
}

func TestNormalizeReadInputExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "file under home", input: "~/notes.md", want: filepath.Join(home, "notes.md")},
		{name: "nested path", input: "~/a/b.md", want: filepath.Join(home, "a", "b.md")},
		{name: "bare tilde", input: "~", want: home},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeReadInput(test.input)
			if got != test.want {
				t.Fatalf("normalizeReadInput() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeReadInputFoldsUnicodeSpaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no-break space", input: "foo\u00A0bar.txt", want: "foo bar.txt"},
		{name: "narrow no-break space", input: "foo\u202Fbar.txt", want: "foo bar.txt"},
		{name: "figure space", input: "foo\u2007bar.txt", want: "foo bar.txt"},
		{name: "thin space", input: "foo\u2009bar.txt", want: "foo bar.txt"},
		{name: "hair space", input: "foo\u200Abar.txt", want: "foo bar.txt"},
		{name: "plain space unchanged", input: "foo bar.txt", want: "foo bar.txt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeReadInput(test.input)
			if got != test.want {
				t.Fatalf("normalizeReadInput() = %q, want %q", got, test.want)
			}
		})
	}
}

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

func TestCurlyApostropheVariant(t *testing.T) {
	t.Parallel()
	if got, want := curlyApostropheVariant("it's.txt"), "it\u2019s.txt"; got != want {
		t.Fatalf("curlyApostropheVariant() = %q, want %q", got, want)
	}
	if got, want := curlyApostropheVariant("plain.txt"), "plain.txt"; got != want {
		t.Fatalf("curlyApostropheVariant() = %q, want %q", got, want)
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

func TestResolveReadTargetFindsNFDVariant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cafe\u0301.txt"), []byte("decomposed"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("caf\u00e9.txt")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	// Byte-exact comparison is meaningless on normalization-insensitive
	// filesystems (APFS/HFS+): an NFC lookup of an NFD name succeeds there.
	// Assert the resolved path names the file we created via its inode.
	wanted, err := os.Stat(filepath.Join(workspace.PhysicalPath(), "cafe\u0301.txt"))
	if err != nil {
		t.Fatalf("os.Stat(variant) error = %v", err)
	}
	resolved, err := os.Stat(got)
	if err != nil {
		t.Fatalf("os.Stat(resolved) error = %v", err)
	}
	if !os.SameFile(resolved, wanted) {
		t.Fatalf("resolveReadTarget() = %q does not name the variant file", got)
	}
}

func TestResolveReadTargetFindsCurlyApostropheVariant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "it\u2019s.txt"), []byte("curly"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("it's.txt")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	if want := filepath.Join(workspace.PhysicalPath(), "it\u2019s.txt"); got != want {
		t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
	}
}

func TestResolveReadTargetFindsCurlyApostropheNFDVariant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// macOS screenshot-style name: U+2019 apostrophe plus decomposed é.
	if err := os.WriteFile(
		filepath.Join(root, "Capture d\u2019e\u0301cran.png"),
		[]byte("screenshot"),
		0o644,
	); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("Capture d'\u00e9cran.png")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	wanted, err := os.Stat(filepath.Join(workspace.PhysicalPath(), "Capture d\u2019e\u0301cran.png"))
	if err != nil {
		t.Fatalf("os.Stat(variant) error = %v", err)
	}
	resolved, err := os.Stat(got)
	if err != nil {
		t.Fatalf("os.Stat(resolved) error = %v", err)
	}
	if !os.SameFile(resolved, wanted) {
		t.Fatalf("resolveReadTarget() = %q does not name the variant file", got)
	}
}

func TestResolveReadTargetFindsMacOSScreenshotVariant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	name := "Shot at 3.42.10\u202FAM.png"
	if err := os.WriteFile(filepath.Join(root, name), []byte("screenshot"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("Shot at 3.42.10 AM.png")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	if want := filepath.Join(workspace.PhysicalPath(), name); got != want {
		t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
	}
}

func TestResolveReadTargetFindsUnicodeSpaceInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo bar.txt"), []byte("space"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("foo\u00A0bar.txt")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	if want := filepath.Join(workspace.PhysicalPath(), "foo bar.txt"); got != want {
		t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
	}
}

func TestResolveReadTargetStripsAtPrefix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("@notes.md")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	if want := filepath.Join(workspace.PhysicalPath(), "notes.md"); got != want {
		t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
	}
}

func TestResolveReadTargetExpandsTilde(t *testing.T) {
	if runtime.GOOS == "windows" && os.Getenv("USERPROFILE") != "" {
		t.Skip("tilde expansion follows the OS home lookup; covered on unix")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	read, _ := newReadForTest(t, root)

	got, err := read.resolveReadTarget("~/hello.txt")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	if want := filepath.Join(home, "hello.txt"); got != want {
		t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
	}
}

func TestResolveReadTargetReturnsBaseWhenNoVariantExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	read, workspace := newReadForTest(t, root)

	got, err := read.resolveReadTarget("missing.txt")
	if err != nil {
		t.Fatalf("resolveReadTarget() error = %v", err)
	}
	if want := filepath.Join(workspace.PhysicalPath(), "missing.txt"); got != want {
		t.Fatalf("resolveReadTarget() = %q, want %q", got, want)
	}
}

func TestResolveReadTargetKeepsOriginalPathError(t *testing.T) {
	t.Parallel()
	read, _ := newReadForTest(t, t.TempDir())

	_, err := read.resolveReadTarget("@")
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("resolveReadTarget() error = %v, want path-required error", err)
	}
}
