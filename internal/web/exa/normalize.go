package exa

import (
	"fmt"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/web"
)

// normalizeResults maps Exa results onto the shared evidence contract.
// Highlights become excerpts, full text a document and a generated summary a
// summary; they are never merged. Rows without an actionable URL are dropped
// with a warning. An upstream empty array is a successful empty result; a
// non-empty array whose rows were all dropped is an invalid response.
func normalizeResults(results []searchResult, retrievedAt time.Time) (evidence.Bundle, error) {
	bundle := evidence.Bundle{}
	if len(results) == 0 {
		return bundle, nil
	}
	retrieved := retrievedAt.UnixMilli()
	sources := make(map[string]int, len(results))
	for index, result := range results {
		source, err := evidence.NewSource(result.URL, result.Title, result.PublishedDate)
		if err != nil {
			bundle.Diagnostics.Warnings = append(bundle.Diagnostics.Warnings, fmt.Sprintf("result %d dropped: %v", index, err))
			continue
		}
		position, seen := sources[source.ID]
		if seen {
			bundle.Diagnostics.Warnings = append(bundle.Diagnostics.Warnings, fmt.Sprintf("result %d repeats %s", index, source.URL))
			if bundle.Sources[position].Title == "" {
				bundle.Sources[position].Title = source.Title
			}
		} else {
			sources[source.ID] = len(bundle.Sources)
			bundle.Sources = append(bundle.Sources, source)
		}
		for _, highlight := range result.Highlights {
			appendItem(&bundle, source.ID, evidence.KindExcerpt, highlight, maxHighlightBytes, retrieved)
		}
		appendItem(&bundle, source.ID, evidence.KindDocument, result.Text, maxDocumentBytes, retrieved)
		appendItem(&bundle, source.ID, evidence.KindSummary, result.Summary, maxSummaryBytes, retrieved)
	}
	if len(bundle.Sources) == 0 {
		return evidence.Bundle{}, &web.Error{Code: web.CodeInvalidResponse, Message: fmt.Sprintf("all %d search results were malformed: %s", len(results), strings.Join(bundle.Diagnostics.Warnings, "; "))}
	}
	if err := bundle.Validate(); err != nil {
		return evidence.Bundle{}, &web.Error{Code: web.CodeInvalidResponse, Message: "normalized search results are invalid", Cause: err}
	}
	return bundle, nil
}

func appendItem(bundle *evidence.Bundle, sourceID string, kind evidence.Kind, text string, limit int, retrievedAt int64) {
	text = strings.TrimSpace(strings.ToValidUTF8(text, "\uFFFD"))
	if text == "" {
		return
	}
	bounded, truncated := web.TruncateUTF8(text, limit)
	bundle.Items = append(bundle.Items, evidence.Evidence{
		SourceID:      sourceID,
		Kind:          kind,
		Text:          bounded,
		Format:        evidence.FormatText,
		Acquisition:   evidence.AcquisitionSearchService,
		RetrievedAt:   retrievedAt,
		Truncated:     truncated,
		ReturnedBytes: len(bounded),
	})
}

func costOf(total float64) *evidence.Cost {
	return &evidence.Cost{Amount: total, Currency: "USD", Source: "exa costDollars.total"}
}
