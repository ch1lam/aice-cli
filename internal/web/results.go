package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/utf8text"
)

// Per-item evidence bounds applied while normalizing service results.
const (
	maxExcerptBytes  = 2 * 1024
	maxDocumentBytes = 8 * 1024
	maxSummaryBytes  = 2 * 1024
)

// ResultRow is one search service result before normalization. Excerpts are
// query-relevant passages, Document is page text and Summary is generated;
// they stay separate evidence kinds.
type ResultRow struct {
	URL       string
	Title     string
	Published string
	Excerpts  []string
	Document  string
	Summary   string
}

// NormalizeResults maps service rows onto the shared evidence contract. Rows
// without an actionable URL are dropped with a warning and repeated URLs merge
// into one source. An upstream empty list is a successful empty result; a
// non-empty list whose rows were all dropped is an invalid response.
func NormalizeResults(rows []ResultRow, retrievedAt time.Time) (evidence.Bundle, error) {
	bundle := evidence.Bundle{}
	if len(rows) == 0 {
		return bundle, nil
	}
	retrieved := retrievedAt.UnixMilli()
	sources := make(map[string]int, len(rows))
	for index, row := range rows {
		source, err := evidence.NewSource(row.URL, row.Title, row.Published)
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
		for _, excerpt := range row.Excerpts {
			appendItem(&bundle, source.ID, evidence.KindExcerpt, excerpt, maxExcerptBytes, retrieved)
		}
		appendItem(&bundle, source.ID, evidence.KindDocument, row.Document, maxDocumentBytes, retrieved)
		appendItem(&bundle, source.ID, evidence.KindSummary, row.Summary, maxSummaryBytes, retrieved)
	}
	if len(bundle.Sources) == 0 {
		return evidence.Bundle{}, &Error{Code: CodeInvalidResponse, Message: fmt.Sprintf("all %d search results were malformed: %s", len(rows), strings.Join(bundle.Diagnostics.Warnings, "; "))}
	}
	if err := bundle.Validate(); err != nil {
		return evidence.Bundle{}, &Error{Code: CodeInvalidResponse, Message: "normalized search results are invalid", Cause: err}
	}
	return bundle, nil
}

func appendItem(bundle *evidence.Bundle, sourceID string, kind evidence.Kind, text string, limit int, retrievedAt int64) {
	text = strings.TrimSpace(strings.ToValidUTF8(text, "�"))
	if text == "" {
		return
	}
	bounded := utf8text.Prefix(text, limit)
	bundle.Items = append(bundle.Items, evidence.Evidence{
		SourceID:      sourceID,
		Kind:          kind,
		Text:          bounded,
		Format:        evidence.FormatText,
		Acquisition:   evidence.AcquisitionSearchService,
		RetrievedAt:   retrievedAt,
		Truncated:     len(bounded) < len(text),
		ReturnedBytes: len(bounded),
	})
}
