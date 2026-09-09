package tool

import (
	"strings"
	"testing"
)

func TestEditDiffHunksAndTerminators(t *testing.T) {
	for _, tt := range []struct{ name, before, after, want string }{
		{"replacement", "one\ntwo\n", "one\nthree\n", "@@ -1,2 +1,2 @@\n one\n-two\n+three\n"},
		{"insertion", "", "new\n", "@@ -0,0 +1,1 @@\n+new\n"},
		{"deletion", "old\n", "", "@@ -1,1 +0,0 @@\n-old\n"},
		{"newline", "a", "a\n", "@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+a\n"},
		{"crlf", "a\r\n", "a\n", "@@ -1,1 +1,1 @@\n-a\r\n+a\n"},
		{"unchanged", "a\n", "a\n", ""},
		{"shifted hunks", "old\n1\n2\n3\n4\n5\n6\n7\nend\n", "new\nadded\n1\n2\n3\n4\n5\n6\n7\nlast\n", "@@ -1,4 +1,5 @@\n-old\n+new\n+added\n 1\n 2\n 3\n@@ -6,4 +7,4 @@\n 5\n 6\n 7\n-end\n+last\n"},
		{"separate", "old\n1\n2\n3\n4\n5\n6\n7\nend\n", "new\n1\n2\n3\n4\n5\n6\n7\nlast\n", "@@ -1,4 +1,4 @@\n-old\n+new\n 1\n 2\n 3\n@@ -6,4 +6,4 @@\n 5\n 6\n 7\n-end\n+last\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := editDiff(tt.before, tt.after)
			if got.Text != tt.want || got.Truncated {
				t.Fatalf("diff = %+v; want %q", got, tt.want)
			}
		})
	}
}

func TestEditDiffBounds(t *testing.T) {
	for _, tt := range []struct{ name, before, after string }{
		{"bytes", "old\n", strings.Repeat("x", editDiffBytes) + "\n"},
		{"rows", strings.Repeat("old\n", 3000), strings.Repeat("new\n", 3000)},
		{"input", strings.Repeat("\n", editDiffInputLines), "x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := editDiff(tt.before, tt.after)
			if !got.Truncated || len(got.Text) > editDiffBytes || strings.Count(got.Text, "\n") > editDiffLines {
				t.Fatalf("unbounded diff: %d bytes", len(got.Text))
			}
		})
	}
}
