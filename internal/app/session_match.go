package app

import (
	"strings"
	"unicode"
)

// A query is prepared once and scans original prose without allocating a
// lowercase copy of each message. KMP keeps repeated prefixes linear too.
// Matching uses ToLower, preserving the catalog's Unicode case semantics.
type sessionTextQuery struct {
	runes    []rune
	fallback []int
}

func newSessionTextQuery(query string) sessionTextQuery {
	runes := []rune(strings.ToLower(query))
	fallback := make([]int, len(runes))
	for i, matched := 1, 0; i < len(runes); i++ {
		for matched > 0 && runes[i] != runes[matched] {
			matched = fallback[matched-1]
		}
		if runes[i] == runes[matched] {
			matched++
		}
		fallback[i] = matched
	}
	return sessionTextQuery{runes: runes, fallback: fallback}
}

// index returns a rune offset so excerpts remain correct when lowercasing
// changes UTF-8 byte widths (for example, İ becomes i).
func (q sessionTextQuery) index(text string) int {
	if len(q.runes) == 0 {
		return 0
	}
	matched, position := 0, 0
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		} else if r >= unicode.MaxASCII {
			r = unicode.ToLower(r)
		}
		for matched > 0 && r != q.runes[matched] {
			matched = q.fallback[matched-1]
		}
		if r == q.runes[matched] {
			matched++
			if matched == len(q.runes) {
				return position + 1 - matched
			}
		}
		position++
	}
	return -1
}
