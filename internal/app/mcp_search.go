package app

import (
	"strings"
	"unicode"
)

// mcpSearchTerms uses words for spaced text and overlapping pairs for Han text,
// allowing a Chinese phrase inside a sentence to match without a dictionary.
// A small fixed action vocabulary connects common discovery paraphrases; it
// does not translate languages or infer execution intent/permission.
func mcpSearchTerms(value string) map[string]bool {
	terms := make(map[string]bool)
	var word, han []rune
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		term := strings.ToLower(string(word))
		word = word[:0]
		switch term {
		case "a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "has", "have", "how", "i", "in", "into", "is", "it", "its", "me", "my", "of", "on", "or", "our", "that", "the", "their", "this", "to", "was", "what", "when", "which", "who", "with":
			return
		}
		// Normalize common English plurals without stemming arbitrary prefixes.
		if len(term) > 4 && strings.HasSuffix(term, "ies") {
			term = strings.TrimSuffix(term, "ies") + "y"
		} else if len(term) > 3 && strings.HasSuffix(term, "s") && !strings.HasSuffix(term, "ss") && !strings.HasSuffix(term, "us") && !strings.HasSuffix(term, "is") {
			term = strings.TrimSuffix(term, "s")
		}
		if len(term) > 6 && strings.HasSuffix(term, "ion") {
			term = strings.TrimSuffix(term, "ion")
		}
		switch term {
		case "add", "make":
			term = "create"
		case "find", "locate":
			term = "search"
		case "show", "get":
			term = "read"
		case "remove":
			term = "delete"
		}
		terms[term] = true
	}
	flushHan := func() {
		if len(han) == 1 {
			terms[string(han)] = true
		}
		for i := 1; i < len(han); i++ {
			terms[string(han[i-1:i+1])] = true
		}
		han = han[:0]
	}
	for _, r := range value {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return terms
}
