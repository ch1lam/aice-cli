package exa

import (
	"time"

	"github.com/ch1lam/aice-cli/internal/evidence"
	"github.com/ch1lam/aice-cli/internal/web"
)

// normalizeResults maps Exa results onto the shared evidence contract:
// highlights become excerpts, full text a document and a generated summary a
// summary.
func normalizeResults(results []searchResult, retrievedAt time.Time) (evidence.Bundle, error) {
	rows := make([]web.ResultRow, len(results))
	for index, result := range results {
		rows[index] = web.ResultRow{URL: result.URL, Title: result.Title, Published: result.PublishedDate,
			Excerpts: result.Highlights, Document: result.Text, Summary: result.Summary}
	}
	return web.NormalizeResults(rows, retrievedAt)
}

func costOf(total float64) *evidence.Cost {
	return &evidence.Cost{Amount: total, Currency: "USD", Source: "exa costDollars.total"}
}
