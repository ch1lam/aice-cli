package tui

import (
	"slices"
	"testing"
)

func TestFuzzyMatchReturnsBestVisibleAlignment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value, query string
		want               []int
		matched            bool
	}{
		{name: "empty query", value: "model", matched: true},
		{name: "gaps", value: "skill-design", query: "sd", want: []int{0, 6}, matched: true},
		{name: "consecutive later match", value: "ax-ab", query: "ab", want: []int{3, 4}, matched: true},
		{name: "case insensitive", value: "Compact", query: "CPT", want: []int{0, 3, 6}, matched: true},
		{name: "Unicode rune offsets", value: "中文模型", query: "中模", want: []int{0, 2}, matched: true},
		{name: "reversed order", value: "model", query: "dm"},
		{name: "missing repeated letter", value: "model", query: "mm"},
		{name: "longer query", value: "hi", query: "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			score, indices := fuzzyMatch(tc.value, tc.query)
			if (score >= 0) != tc.matched || !slices.Equal(indices, tc.want) {
				t.Fatalf("match = %d, %v; want matched %v, positions %v", score, indices, tc.matched, tc.want)
			}
		})
	}
}

func TestFuzzySuggestionsRankRelevanceBeforeCatalogOrder(t *testing.T) {
	t.Parallel()
	commands := []SlashCommand{{Name: "m-o-d"}, {Name: "remodel"}, {Name: "model"}, {Name: "mod"}}
	matches := matchingSlashCommands(commands, "/mod")
	for i, want := range []string{"mod", "model", "remodel", "m-o-d"} {
		if len(matches) != 4 || matches[i].Name != want {
			t.Fatalf("ranked suggestions = %#v, want exact, prefix, substring, scattered", matches)
		}
	}
	if matches := matchingSlashCommands(commands, "/"); matches[0].Name != "m-o-d" {
		t.Fatal("empty query changed catalog order")
	}
}
