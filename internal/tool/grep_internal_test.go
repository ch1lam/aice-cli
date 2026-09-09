package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepContextFailurePreservesStreamedMatch(t *testing.T) {
	t.Parallel()
	for _, shortened := range []bool{false, true} {
		name := "missing"
		if shortened {
			name = "shortened"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "target.txt")
			if shortened {
				if err := os.WriteFile(path, []byte("now shorter"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			match := grepMatch{filePath: path, lineNumber: 3, lineText: strings.Repeat("界", 501) + "\n"}
			output, capped, long := formatGrepMatches([]grepMatch{match}, root, true, 1)
			if capped || !long || !strings.Contains(output, "target.txt:3: "+strings.Repeat("界", 500)+"... [truncated]") || !strings.Contains(output, "context unavailable:") {
				t.Fatalf("lost match or diagnostic: %q (capped=%v long=%v)", output, capped, long)
			}
		})
	}
}

func TestGrepContextChangedLinePreservesOriginalMatch(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, content string }{
		{name: "rewritten", content: "before\nnew text\nafter\n"},
		{name: "shortened with trailing newline", content: "before\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "changed.txt")
			if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}
			matches := []grepMatch{{filePath: path, lineNumber: 2, lineText: "needle original\n"}}
			output, capped, long := formatGrepMatches(matches, root, true, 1)
			if capped || long || !strings.Contains(output, "changed.txt:2: needle original") || !strings.Contains(output, "context unavailable: matched line changed since search") || strings.Contains(output, "new text") {
				t.Fatalf("changed content mislabeled as a match: %q", output)
			}
		})
	}
}
