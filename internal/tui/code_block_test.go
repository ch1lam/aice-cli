package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCodeBlockRetainsSourceAndLineMapping(t *testing.T) {
	for _, source := range []string{"", "\n", "a\n\n", "\t中文😀é\r\nlast", "a  b  ", "\x1b]52;c;payload\a\u202e"} {
		for _, width := range []int{6, 9, 24, 80} {
			t.Run(fmt.Sprintf("%q/%d", source, width), func(t *testing.T) {
				block := newCodeBlock(source, "text")
				layout := block.layout(codeBlockOptions{width: width})
				if layout.block.source != source || strings.Join(block.lines, "") != source {
					t.Fatal("display changed original bytes")
				}
				assertToolBackground(t, layout.view())
				recovered := make([]string, len(block.lines))
				for _, row := range layout.rows {
					if ansi.StringWidth(row.text) != width {
						t.Fatalf("row exceeds panel: %q", row.text)
					}
					if row.sourceLine < 0 {
						continue
					}
					// The display may have right padding. Each original line's
					// escaped prefix must survive every narrow-width wrap.
					plain := ansi.Strip(ansi.Cut(row.text, layout.contentColumn, width-2))
					recovered[row.sourceLine] += strings.TrimRight(plain, " ")
				}
				for i, line := range block.lines {
					want := strings.ReplaceAll(escapeCodeRow(strings.TrimSuffix(line, "\n")), " ", "")
					got := strings.ReplaceAll(recovered[i], " ", "")
					if got != want {
						t.Fatalf("line %d: got %q, want %q", i, got, want)
					}
				}
			})
		}
	}
}

func TestCodeBlockClipRetainsUnclippedSource(t *testing.T) {
	source := strings.Repeat("long", 100) + "\r\n\nlast\n"
	layout := newCodeBlock(source, "go").layout(codeBlockOptions{width: 24, clip: true})
	if len(layout.block.lines) != 3 || layout.block.source != source {
		t.Fatal("clipping lost source or invented a trailing line")
	}
	if len(layout.rows) != 5 || !strings.Contains(layout.view(), "…") {
		t.Fatal("preview must clip to one visual row per source line")
	}
	if layout.rows[0].sourceLine != -1 || layout.rows[4].sourceLine != -1 {
		t.Fatal("padding points at source")
	}
	assertToolBackground(t, layout.view())
}

func TestCodeBlockNumbersSourceLinesAndCounts(t *testing.T) {
	block := newCodeBlock(strings.Repeat("x", 40)+"\n\nlast\n", "go")
	layout := block.layout(codeBlockOptions{width: 24})
	if !strings.Contains(ansi.Strip(layout.rows[0].text), "3 lines") {
		t.Fatal("header must count source lines, not wrapped rows")
	}
	seen := make(map[int]bool)
	for _, row := range layout.rows {
		if row.sourceLine < 0 {
			continue
		}
		gutter := strings.TrimSpace(ansi.Strip(ansi.Cut(row.text, 2, layout.contentColumn)))
		want := fmt.Sprint(row.sourceLine + 1)
		if seen[row.sourceLine] {
			want = ""
		}
		if gutter != want {
			t.Fatalf("line %d gutter = %q, want %q", row.sourceLine, gutter, want)
		}
		seen[row.sourceLine] = true
	}
	if len(seen) != 3 {
		t.Fatal("blank source line was not numbered")
	}
	partial := block.layout(codeBlockOptions{width: 40, incomplete: true})
	if !strings.Contains(ansi.Strip(partial.rows[0].text), "3 lines · partial") {
		t.Fatal("partial input claimed a complete total")
	}
	if narrow := block.layout(codeBlockOptions{width: 9}); narrow.contentColumn != 2 {
		t.Fatal("narrow panel must prioritize source over numbers")
	}
}

func TestCodeBlockHighlightingPreservesLiteralMarkdown(t *testing.T) {
	for _, language := range []string{"md", "text", "unrecognized-language"} {
		source := "# title\n\n```go\nvar text = `raw`\n```\n"
		layout := newCodeBlock(source, language).layout(codeBlockOptions{width: 60})
		if layout.block.source != source || !strings.Contains(ansi.Strip(layout.view()), "```go") {
			t.Fatalf("%s interpreted source as Markdown", language)
		}
		assertToolBackground(t, layout.view())
	}
}

func TestCodeBlockWrappedRowsRetainTokenColor(t *testing.T) {
	layout := newCodeBlock("// "+strings.Repeat("comment ", 30), "go").layout(codeBlockOptions{width: 24})
	for _, row := range layout.rows {
		if row.sourceLine < 0 {
			continue
		}
		if !strings.Contains(row.text, "\x1b[38;2;143;132;119m") {
			t.Fatalf("wrapped comment lost its foreground: %q", row.text)
		}
	}
}

func TestCodeLinePrefixPreservesTerminators(t *testing.T) {
	for _, tc := range []struct {
		source, want string
		limited      bool
	}{
		{"", "", false}, {"a", "a", false}, {"a\n", "a\n", false},
		{"a\r\n", "a\r\n", false}, {"a\n\n", "a\n", true},
		{"a\nsecond", "a\n", true},
	} {
		t.Run(fmt.Sprintf("%q", tc.source), func(t *testing.T) {
			got, limited := codeLinePrefix(tc.source, 1)
			if got != tc.want || limited != tc.limited {
				t.Fatalf("got %q/%v", got, limited)
			}
		})
	}
}

func BenchmarkCodeBlockLongLine(b *testing.B) {
	for _, size := range []int{4096, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			block := newCodeBlock(strings.Repeat("x", size), "text")
			b.ReportAllocs()
			for b.Loop() {
				block.layout(codeBlockOptions{width: 24})
			}
		})
	}
}
