package app

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/ch1lam/aice-cli/internal/agent"
	"github.com/ch1lam/aice-cli/internal/tool"
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

// mcpSearchDocument is a value-only projection of a discovered tool. Retrieval
// neither reads live catalog state nor grants execution permission.
type mcpSearchDocument struct {
	reference   agent.ToolReference
	service     string
	name        string
	remoteName  string
	description string
}

func rankMCPTools(documents map[string]mcpSearchDocument, request tool.ToolSearchRequest, response tool.ToolSearchResult) tool.ToolSearchResult {
	type candidate struct {
		entry mcpSearchDocument
		terms map[string]bool
		score float64
		exact bool
	}
	var candidates []candidate
	terms := mcpSearchTerms(request.Query)
	queryTerms := make([]string, 0, len(terms))
	for term := range terms {
		queryTerms = append(queryTerms, term)
	}
	slices.Sort(queryTerms)
	frequencies := make(map[string]int)
	for id, entry := range documents {
		if request.Service != "" && entry.service != request.Service || len(request.IDs) > 0 && !slices.Contains(request.IDs, id) {
			continue
		}
		documentTerms := mcpSearchTerms(entry.description)
		for term := range mcpSearchTerms(entry.remoteName) {
			documentTerms[term] = true
		}
		for term := range documentTerms {
			frequencies[term]++
		}
		candidates = append(candidates, candidate{entry: entry, terms: documentTerms, exact: strings.EqualFold(strings.TrimSpace(request.Query), entry.remoteName)})
	}
	// Common catalog boilerplate and repeated domain words carry less evidence
	// than rare capabilities. Count each term once so repetition cannot boost it.
	for i := range candidates {
		for _, term := range queryTerms {
			if candidates[i].terms[term] {
				candidates[i].score += math.Log1p(float64(len(candidates)) / float64(frequencies[term]))
			}
		}
	}
	candidates = slices.DeleteFunc(candidates, func(c candidate) bool {
		return len(terms) > 0 && c.score == 0 && !c.exact && len(request.IDs) == 0
	})
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.exact != b.exact {
			if a.exact {
				return -1
			}
			return 1
		}
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return strings.Compare(a.entry.reference.ID, b.entry.reference.ID)
	})
	start := min(request.Offset, len(candidates))
	end := min(start+request.Limit, len(candidates))
	for _, hit := range candidates[start:end] {
		entry := hit.entry
		description := entry.description
		if len(description) > 2048 {
			description = string([]rune(description)[:min(512, len([]rune(description)))]) + "…"
		}
		response.Entries = append(response.Entries, tool.ToolSearchEntry{ID: entry.reference.ID, Name: entry.name, Service: entry.service, Description: description, Status: "available; execution permission checked separately"})
		response.Selected = append(response.Selected, entry.reference)
	}
	if len(candidates) > end {
		response.Complete = false
		response.NextOffset = &end
		response.Notices = append(response.Notices, "More tools match; continue with next_offset, narrow query, browse a service or select exact IDs. Catalog changes may move page boundaries; use exact IDs for stable selection.")
	}
	for _, id := range request.IDs {
		if _, exists := documents[id]; !exists {
			response.Complete = false
			response.Notices = append(response.Notices, "An exact tool ID is unavailable; inspect service status and refresh discovery.")
			break
		}
	}
	return response
}
