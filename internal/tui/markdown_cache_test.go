package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMarkdownCacheMatchesFullRender(t *testing.T) {
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
				var cache markdownCache
				// Include partial UTF-8 and incomplete delimiters, like arbitrary
				// stream chunk boundaries. Also revisit shorter and changed input.
				for end := 1; end <= len(source); end++ {
					assertMarkdownCacheEqual(t, &cache, source[:end], width)
				}
				assertMarkdownCacheEqual(t, &cache, source, width+13)
				assertMarkdownCacheEqual(t, &cache, source[:len(source)/2], width)
				assertMarkdownCacheEqual(t, &cache, "replacement\n\n"+source, width)
			})
		}
	}
}

func assertMarkdownCacheEqual(t *testing.T, cache *markdownCache, source string, width int) {
	t.Helper()
	got, err := cache.render(source, width)
	if err != nil {
		t.Fatal(err)
	}
	want, err := renderMarkdownBlocks(source, width)
	if err != nil {
		t.Fatal(err)
	}
	if got.view != want.view {
		t.Fatalf("source %q\ngot  %q\nwant %q", source, got.view, want.view)
	}
	if !reflect.DeepEqual(got.blocks, want.blocks) {
		t.Fatalf("source %q: code layout/copy geometry differs", source)
	}
}

func TestMarkdownCacheReusesStableGroups(t *testing.T) {
	var cache markdownCache
	source := "first\n\n```go\nvar value = 42\n```\n\nsecond\n\nlast"
	assertMarkdownCacheEqual(t, &cache, source, 80)
	if len(cache.parts) < 3 {
		t.Fatalf("expected stable groups, got %d", len(cache.parts))
	}
	before := cache.parts[0].content.blocks
	assertMarkdownCacheEqual(t, &cache, source+" appended", 80)
	if len(before) != 1 || &before[0] != &cache.parts[0].content.blocks[0] {
		t.Fatal("unchanged code block was laid out again")
	}
	assertMarkdownCacheEqual(t, &cache, strings.ReplaceAll(source, "first", "changed"), 80)
	if &before[0] == &cache.parts[0].content.blocks[0] {
		t.Fatal("changed group retained stale content")
	}
}

func FuzzMarkdownCacheMatchesFullRender(f *testing.F) {
	for _, source := range []string{
		"first\n\nsecond\n\nlast",
		"before\n\n```go\ncode\n```\n\nafter",
		"before\n\n> quote\n\nafter",
		"first\n\n- one\n- two\n\nafter",
		"[link][ref]\n\ntext\n\n[ref]: https://example.com\n\nlast",
	} {
		f.Add(source, uint16(len(source)/2))
	}
	f.Fuzz(func(t *testing.T, source string, split uint16) {
		if len(source) > 2048 {
			t.Skip()
		}
		var cache markdownCache
		assertMarkdownCacheEqual(t, &cache, source[:int(split)%(len(source)+1)], 40)
		assertMarkdownCacheEqual(t, &cache, source, 40)
	})
}
