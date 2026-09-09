package tool_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ch1lam/aice-cli/internal/tool"
)

func TestLSExecuteListsSortedEntries(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "b.txt", "b")
	if err := os.Mkdir(filepath.Join(root, "a"), 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	if err := os.Symlink("b.txt", filepath.Join(root, "link")); err != nil {
		t.Skipf("os.Symlink() error = %v", err)
	}
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatalf("NewLS() error = %v", err)
	}

	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "a/\nb.txt\nlink@"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestLSExecuteEnforcesLimit(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	writeFixture(t, root, "a.txt", "a")
	writeFixture(t, root, "b.txt", "b")
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatalf("NewLS() error = %v", err)
	}

	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{"limit": 1}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := resultText(t, result); !strings.Contains(got, "entry limit reached") {
		t.Fatalf("Execute() text = %q, want limit marker", got)
	}
}

func TestLSExecuteListsParentDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixture(t, parent, "outside.txt", "outside")
	workspace := newWorkspaceAt(t, root)
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatalf("NewLS() error = %v", err)
	}

	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{"path": ".."}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := resultText(t, result), "outside.txt\nwork/"; got != want {
		t.Fatalf("Execute() text = %q, want %q", got, want)
	}
}

func TestLSExecuteTruncatesWholeEntries(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	names := make(map[string]bool)
	for i := range 501 {
		name := fmt.Sprintf("%03d-%s.txt", i, strings.Repeat("界", 60))
		writeFixture(t, root, name, "")
		names[name] = true
	}
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	output := resultText(t, result)
	if len(output) > 50*1024 || !utf8.ValidString(output) {
		t.Fatalf("invalid output: %d bytes, UTF-8 valid=%v", len(output), utf8.ValidString(output))
	}
	if !strings.Contains(output, "[output truncated: 50 KiB limit reached;") ||
		!strings.Contains(output, "entry limit reached") {
		t.Fatalf("missing truncation notices: %q", output)
	}
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "[") {
			continue
		}
		if !names[line] {
			t.Fatalf("output contains incomplete or unknown entry %q", line)
		}
		count++
	}
	if count == 0 || count >= 500 {
		t.Fatalf("returned %d entries, want a nonempty byte-limited subset", count)
	}
}

func TestLSExecuteLimitGuidance(t *testing.T) {
	t.Parallel()
	workspace, root := newWorkspace(t)
	for i := range 501 {
		writeFixture(t, root, fmt.Sprintf("f%03d", i), "")
	}
	ls, err := tool.NewLS(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name      string
		limit     int
		wantCount int
		want      string
		wantError bool
	}{
		{name: "smaller limit", limit: 1, wantCount: 1, want: "retry with limit=2"},
		{name: "retry capped", limit: 300, wantCount: 300, want: "retry with limit=500"},
		{name: "hard maximum", limit: 500, wantCount: 500, want: "hard maximum"},
		{name: "legacy zero default", limit: 0, wantCount: 500, want: "hard maximum"},
		{name: "excessive", limit: 501, wantError: true, want: "limit cannot exceed 500"},
		{name: "negative", limit: -1, wantError: true, want: "limit cannot be negative"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{"limit": tt.limit}))
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error = %v, want %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			output := resultText(t, result)
			if !strings.Contains(output, tt.want) {
				t.Fatalf("missing %q in %q", tt.want, output)
			}
			lines := strings.Split(output, "\n")
			if got := len(lines) - 1; got != tt.wantCount {
				t.Fatalf("returned %d entries, want %d", got, tt.wantCount)
			}
			if tt.wantCount == 500 && (!strings.Contains(output, "find") || !strings.Contains(output, "bash")) {
				t.Fatal("hard-limit notice must explain how to continue")
			}
		})
	}
}

func TestLSExecuteEmptyAndHiddenEntries(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		populate bool
		want     string
	}{
		{name: "empty", want: "(empty directory)"},
		{name: "hidden entries at exact limit", populate: true, want: ".hidden-dir/\n.hidden-file"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			workspace, root := newWorkspace(t)
			if tt.populate {
				writeFixture(t, root, ".hidden-file", "")
				if err := os.Mkdir(filepath.Join(root, ".hidden-dir"), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			ls, err := tool.NewLS(workspace)
			if err != nil {
				t.Fatal(err)
			}
			result, err := ls.Execute(t.Context(), toolCall(t, "ls", map[string]any{"limit": 2}))
			if err != nil {
				t.Fatal(err)
			}
			if got := resultText(t, result); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}
}
