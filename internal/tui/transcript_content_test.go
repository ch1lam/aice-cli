package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTranscriptPaddingMatchesStyle(t *testing.T) {
	for _, source := range []string{
		"", "short", "short\nlonger\n", "\n\n", "a\r\nb\tend",
		"中文😀\nx", "e\u0301 👩‍💻\nsecond", "\x1b[31mred\x1b[m\nplain",
		layoutMarkdown("# Title\n\ntext\n\n```go\nvar answer = 42\n```", 40).view,
	} {
		for _, padding := range [][2]int{{0, 0}, {1, 1}, {3, 0}, {0, 2}} {
			got := padTranscript(source, padding[0], padding[1])
			want := lipgloss.NewStyle().PaddingLeft(padding[0]).PaddingRight(padding[1]).Render(source)
			if got != want {
				t.Fatalf("source %q padding %v:\ngot %q\nwant %q", source, padding, got, want)
			}
		}
	}
}
