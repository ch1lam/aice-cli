package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHistoryMarkdownPreservesStructureAndCodeGeometry(t *testing.T) {
	fixtures := []string{
		"# Title\n\nFirst **bold** and `code`.\n\nSecond 中文😀 paragraph.\n\nThird\n===\n\nlast",
		"before\n\n```go\npackage main\n```\n\nafter\n\n```\n```\n\nlast",
		"before\n\n- one\n- two\n\n  continued\n\n  > quote\n  >\n  > ```go\n  > code\n  > ```\n\nafter",
		"[label][target]\n\nsecond\n\n[target]: https://example.com\n  \"title\"\n\nafter",
		"first\n\nsecond\n\n---\n\nafter\n\n<div>\nraw html\n</div>\n\nlast",
		"first\n\nterm\n: definition\n\nnext\n\n| a | b |\n| - | - |\n| x | y |\n\nlast",
		"first\r\n\r\n### Title\r\n\r\n    code\r\n\r\nafter",
		"first\n\n```\nsame\n```\n\nsecond\n\n```\nsame\n```\n\n\ue000 &#57345;\n\nlast",
	}
	for i, source := range fixtures {
		for _, width := range []int{24, 80} {
			t.Run(fmt.Sprintf("%d/%d", i, width), func(t *testing.T) {
				h, err := parseHistoryMarkdown(source)
				if err != nil {
					t.Fatal(err)
				}
				var got transcriptContent
				for part := range h.parts {
					got.append(h.content(part, width), "\n")
				}
				want := layoutMarkdown(source, width)
				if ansi.Strip(got.view) != ansi.Strip(want.view) {
					t.Fatalf("render differs:\ngot %q\nwant %q", ansi.Strip(got.view), ansi.Strip(want.view))
				}
				if !reflect.DeepEqual(got.blocks, want.blocks) {
					t.Fatal("code source/geometry differs")
				}
			})
		}
	}
}

func TestHistoryMarkdownFirstViewAndSearchLeaveEarlierPartsUnrendered(t *testing.T) {
	m := newModel(nil, nil)
	m.width, m.height = 80, 20
	m.resizeLayout()
	source := strings.Repeat("A **formatted** paragraph.\n\n", 2000) + "Unique match 中文.\n\nLast paragraph."
	m.entries = []transcriptEntry{{kind: entryAssistant, text: source, complete: true, conclusion: true, presentation: &assistantPresentation{}}}
	m.refreshViewport(true)
	m.View()
	parts := m.viewport.items[0].parts
	if len(parts) < 1000 {
		t.Fatalf("parts = %d", len(parts))
	}
	for i := 0; i < len(parts)-30; i++ {
		if parts[i].lines != nil {
			t.Fatalf("unseen part %d laid out", i)
		}
	}
	m.jumpReadingEntry(0, "Unique match")
	if !strings.Contains(ansi.Strip(m.viewport.View()), "Unique match") {
		t.Fatal("match not visible")
	}
	if parts[1].lines != nil {
		t.Fatal("search laid out earlier prose")
	}
	m.viewport.GotoTop()
	if !strings.Contains(ansi.Strip(m.viewport.View()), "formatted") {
		t.Fatal("beginning missing")
	}
}

func TestTranscriptViewportPartsScrollAndResize(t *testing.T) {
	calls := make([]int, 60)
	makeItems := func() []transcriptItem {
		items := make([]transcriptItem, 3)
		for i := range items {
			items[i] = transcriptItem{key: i, version: i, gap: 1, split: func() []transcriptItem {
				parts := make([]transcriptItem, 20)
				for j := range parts {
					parts[j].render = func() string { calls[i*20+j]++; return fmt.Sprintf("%d.%d\nsecond", i, j) }
				}
				return parts
			}}
		}
		return items
	}
	v := newTranscriptViewport()
	v.SetHeight(4)
	v.setItems(makeItems())
	v.GotoBottom()
	v.View()
	for _, count := range calls[:58] {
		if count != 0 {
			t.Fatal("offscreen part rendered")
		}
	}
	all := strings.Split(v.GetContent(), "\n")
	offset := len(all) - v.Height()
	for _, delta := range []int{-1, -39, 2, -50, -100, 59, 500, -1} {
		v.scroll(delta)
		offset = min(max(0, offset+delta), len(all)-v.Height())
		got := strings.Split(ansi.Strip(v.View()), "\n")
		for i, want := range all[offset : offset+v.Height()] {
			if strings.TrimRight(got[i], " ") != want {
				t.Fatalf("delta %d got %q want %q", delta, got[i], want)
			}
		}
	}
	index, part, line := v.index, v.part, v.line
	v.SetWidth(70)
	v.setItems(makeItems())
	if v.index != index || v.part != part || v.line != line {
		t.Fatal("resize lost part anchor")
	}
	if !strings.Contains(v.View(), "2.18") {
		t.Fatal("resize lost content")
	}
	v.setItems([]transcriptItem{staticTranscriptItem(2, "replacement")})
	if v.part != 0 || !strings.Contains(v.View(), "replacement") {
		t.Fatal("replacement retained invalid part")
	}
}
