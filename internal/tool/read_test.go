package tool_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestReadExecuteReturnsSelectedLines(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	absolutePath := writeFixture(t, root, "notes.txt", "one\ntwo\nthree\nfour\n")
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": absolutePath, "offset": 2, "limit": 2,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "two\nthree\n\n[Showing lines 2-3. Use offset=4 to continue.]"
	if got := resultText(t, result); got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestReadExecuteStripsAtPrefix(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "notes.txt", "notes content\n")
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{"path": "@notes.txt"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "notes content\n"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestReadExecuteResolvesNFDStoredFilename(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	// macOS stores accented filenames in NFD; the model may emit NFC.
	writeFixture(t, root, "cafe\u0301.txt", "decomposed content\n")
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{"path": "caf\u00e9.txt"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "decomposed content\n"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestReadExecuteUsesHostPaths(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	workspace := newWorkspaceAt(t, root)
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	outside := writeFixture(t, parent, "secret.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "relative traversal", path: "../secret.txt"},
		{name: "absolute outside path", path: outside},
		{name: "symlink", path: "escape"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := read.Execute(
				t.Context(),
				toolCall(t, "read", map[string]any{"path": test.path}),
			)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := resultText(t, result), "secret"; got != want {
				t.Fatalf("Execute() text = %q, want %q", got, want)
			}
		})
	}
}

func TestReadExecuteResolvesParentAfterWorkingDirectorySymlink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	physicalParent := filepath.Join(base, "physical")
	physicalWork := filepath.Join(physicalParent, "work")
	if err := os.MkdirAll(physicalWork, 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	writeFixture(t, physicalParent, "sibling.txt", "physical sibling")
	linkedWork := filepath.Join(base, "linked-work")
	if err := os.Symlink(physicalWork, linkedWork); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}

	workspace := newWorkspaceAt(t, linkedWork)
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": "../sibling.txt",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "physical sibling"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestReadExecuteRejectsBinaryAndUnknownArguments(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "binary.dat", "a\x00b")
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	_, err = read.Execute(t.Context(), toolCall(t, "read", map[string]any{"path": "binary.dat"}))
	if err == nil || !strings.Contains(err.Error(), "binary file") {
		t.Fatalf("Execute() error = %v, want binary-file error", err)
	}
	_, err = read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": "binary.dat", "unexpected": true,
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Execute() error = %v, want unknown-field error", err)
	}
}

func TestReadExecuteDoesNotReturnPartialLongLine(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "large.txt", strings.Repeat("a", 60*1024))
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}
	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{"path": "large.txt"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if len(text) > 50*1024 || !strings.Contains(text, "Line 1") ||
		!strings.Contains(text, "byte-bounded command") ||
		strings.Contains(text, strings.Repeat("a", 32)) {
		t.Fatalf("Execute() output = %q", text)
	}
}

func TestReadExecuteBoundsOutputAtCompleteLines(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	line := strings.Repeat("x", 200)
	writeFixture(t, root, "many-lines.txt", strings.Repeat(line+"\n", 400))
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(
		t.Context(),
		toolCall(t, "read", map[string]any{"path": "many-lines.txt"}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if len(text) > 50*1024 {
		t.Fatalf("Execute() output length = %d, want <= %d", len(text), 50*1024)
	}
	content, notice, found := strings.Cut(text, "\n\n[Showing")
	if !found {
		t.Fatalf("Execute() output lacks continuation notice: %q", text[max(0, len(text)-160):])
	}
	if !strings.Contains(notice, "(50 KiB limit). Use offset=") {
		t.Fatalf("Execute() notice = %q", notice)
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for index, got := range lines {
		if got != line {
			t.Fatalf("Execute() line %d length = %d, want complete line length %d", index+1, len(got), len(line))
		}
	}
	nextOffset := len(lines) + 1
	wantNotice := fmt.Sprintf(
		"[Showing lines 1-%d (50 KiB limit). Use offset=%d to continue.]",
		len(lines),
		nextOffset,
	)
	if !strings.HasSuffix(text, wantNotice) {
		t.Fatalf("Execute() suffix = %q, want %q", text[max(0, len(text)-160):], wantNotice)
	}

	nextResult, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": "many-lines.txt", "offset": nextOffset, "limit": 1,
	}))
	if err != nil {
		t.Fatalf("Execute(next page) error = %v", err)
	}
	if nextText := resultText(t, nextResult); !strings.HasPrefix(nextText, line+"\n\n[Showing") {
		t.Fatalf("Execute(next page) text = %q", nextText)
	}
}

func TestReadExecuteAppliesDefaultLineLimit(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	var content strings.Builder
	for lineNumber := 1; lineNumber <= 2001; lineNumber++ {
		fmt.Fprintf(&content, "line-%04d\n", lineNumber)
	}
	writeFixture(t, root, "over-2000-lines.txt", content.String())
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(
		t.Context(),
		toolCall(t, "read", map[string]any{"path": "over-2000-lines.txt"}),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "line-2000\n\n[Showing lines 1-2000 (2000 line limit). Use offset=2001 to continue.]") {
		t.Fatalf("Execute() suffix = %q", text[max(0, len(text)-160):])
	}
	if strings.Contains(text, "line-2001") {
		t.Fatalf("Execute() unexpectedly includes line 2001")
	}
}

func TestReadExecutePaginatesPastTenMiB(t *testing.T) {
	workspace, root := newWorkspace(t)
	writeFixture(
		t,
		root,
		"over-ten-mib.txt",
		strings.Repeat("x", 10*1024*1024+1)+"\ntarget\n",
	)
	read, err := tool.NewRead(workspace)
	if err != nil {
		t.Fatalf("NewRead() error = %v", err)
	}

	result, err := read.Execute(t.Context(), toolCall(t, "read", map[string]any{
		"path": "over-ten-mib.txt", "offset": 2, "limit": 1,
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "target\n"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}
