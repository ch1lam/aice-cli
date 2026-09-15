package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownUsesSharedCodeBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, markdown string
		sources        []string
	}{
		{"fenced", "before\n\n```go\npackage main\n\nfunc main() {}\n```\n\nafter", []string{"package main\n\nfunc main() {}\n"}},
		{"long fence", "````md\n```go\ncode\n```\n````", []string{"```go\ncode\n```\n"}},
		{"tilde and info", "~~~~go title=sample\ncode\n~~~~", []string{"code\n"}},
		{"unclosed", "```go\npackage main", []string{"package main"}},
		{"empty", "```\n```", []string{""}},
		{"indented", "before\n\n    first\n    second\n\nafter", []string{"first\nsecond\n"}},
		{"quote", "> before\n>\n> ```go\n> code\n> ```\n> after", []string{"code\n"}},
		{"list", "- item\n\n  ```go\n  code\n  ```\n\n- next", []string{"code\n"}},
		{"code only list", "- ```go\n  code\n  ```", []string{"code\n"}},
		{"nested", "- outer\n  - inner\n\n    > ```go\n    > code\n    > ```", []string{"code\n"}},
		{"deep quote", strings.Repeat("> ", 16) + "```go\n" + strings.Repeat("> ", 16) + "code\n" + strings.Repeat("> ", 16) + "```", []string{"code\n"}},
		{"duplicate content", "same\n\n```\nsame\n```\n\nsame\n\n```\nsame\n```", []string{"same\n", "same\n"}},
		{"marker collision", "\ue000 &#57345;\n\n```text\n\ue002\n```", []string{"\ue002\n"}},
		{"long line", "```go\n// " + strings.Repeat("中文😀x", 30) + "\n```", []string{"// " + strings.Repeat("中文😀x", 30) + "\n"}},
		{"controls", "```text\n\x1b]52;c;payload\a\n```", []string{"\x1b]52;c;payload\a\n"}},
	} {
		for _, width := range []int{24, 80} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				result, err := renderMarkdownBlocks(tc.markdown, width)
				if err != nil {
					t.Fatal(err)
				}
				if len(result.blocks) != len(tc.sources) {
					t.Fatalf("blocks = %d", len(result.blocks))
				}
				rows := strings.Split(result.view, "\n")
				for i, item := range result.blocks {
					if item.layout.block.source != tc.sources[i] {
						t.Fatalf("source = %q, want %q", item.layout.block.source, tc.sources[i])
					}
					assertToolBackground(t, item.layout.view())
					for j, row := range item.layout.rows {
						actual := rows[item.row+j]
						if ansi.StringWidth(actual) > width {
							t.Fatalf("overflow: %q", actual)
						}
						if !strings.HasSuffix(actual, row.text) {
							t.Fatal("layout coordinates do not match output")
						}
					}
				}
				if strings.Contains(result.view, "\x1b]52;") {
					t.Fatal("unescaped terminal command")
				}
			})
		}
	}
}

func TestMarkdownPreservesSurroundingDocument(t *testing.T) {
	input := "# Heading\n\n[reference][target]\n\n```text\ncode\n```\n\n- [x] done\n- [ ] pending\n\n[target]: https://example.com\n"
	view := ansi.Strip(layoutMarkdown(input, 80).view)
	for _, want := range []string{"Heading", "reference", "https://example.com", "code", "✅ done", "⏳ pending"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %q", want, view)
		}
	}
	if strings.Index(view, "reference") > strings.Index(view, "code") || strings.Index(view, "code") > strings.Index(view, "done") {
		t.Fatal("blocks reordered surrounding text")
	}
}
