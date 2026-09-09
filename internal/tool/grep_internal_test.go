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
