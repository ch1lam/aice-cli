package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSessionTextQuery(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, text, query string }{
		{"empty", "中文", ""},
		{"missing", "Some text", "absent"},
		{"first occurrence", "NEEDLE then needle", "needle"},
		{"unicode widths", "İ中文KELVIN", "kelvin"},
		{"unicode query", "prefix İ中文 suffix", "İ中文"},
		{"lowercase semantics", "ς σ", "Σ"},
		{"overlap", "ababababac", "ababac"},
		{"repeated prefix", strings.Repeat("a", 10000) + "b", strings.Repeat("a", 256) + "b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertSessionTextQuery(t, test.text, test.query)
		})
	}
}

func FuzzSessionTextQuery(f *testing.F) {
	f.Add("İ中文 NEEDLE needle", "needle")
	f.Add("ababababac", "ababac")
	f.Fuzz(func(t *testing.T, text, query string) {
		// Session JSON decoding supplies valid UTF-8 prose.
		if !utf8.ValidString(text) || !utf8.ValidString(query) {
			t.Skip()
		}
		assertSessionTextQuery(t, text, query)
	})
}

func assertSessionTextQuery(t *testing.T, text, query string) {
	t.Helper()
	lower := strings.ToLower(text)
	want := strings.Index(lower, strings.ToLower(query))
	if want >= 0 {
		want = utf8.RuneCountInString(lower[:want])
	}
	if got := newSessionTextQuery(query).index(text); got != want {
		t.Fatalf("match %q in %q = %d, want %d", query, text, got, want)
	}
}
