package evidence

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Validate checks referential integrity, closed enumerations, valid UTF-8 and
// the encoded size bound so a Bundle can be retained as Session metadata.
func (b *Bundle) Validate() error {
	if b == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(b.Sources))
	for index, source := range b.Sources {
		if err := source.Validate(); err != nil {
			return fmt.Errorf("evidence: source %d: %w", index, err)
		}
		if _, dup := seen[source.ID]; dup {
			return fmt.Errorf("evidence: source %d: duplicate id %q", index, source.ID)
		}
		seen[source.ID] = struct{}{}
	}
	for index, item := range b.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("evidence: item %d: %w", index, err)
		}
		if _, ok := seen[item.SourceID]; !ok {
			return fmt.Errorf("evidence: item %d: unknown source %q", index, item.SourceID)
		}
	}
	for index, warning := range b.Diagnostics.Warnings {
		if !utf8.ValidString(warning) {
			return fmt.Errorf("evidence: warning %d is not valid UTF-8", index)
		}
	}
	if b.Diagnostics.ReportedCost != nil && b.Diagnostics.ReportedCost.Currency == "" {
		return fmt.Errorf("evidence: reported cost requires a currency")
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("evidence: encode bundle: %w", err)
	}
	if len(encoded) > MaxBundleBytes {
		return fmt.Errorf("evidence: bundle is %d bytes, limit %d", len(encoded), MaxBundleBytes)
	}
	return nil
}

// Validate checks one source's identity and URL.
func (s Source) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("source id is required")
	}
	normalized, err := NormalizeURL(s.URL)
	if err != nil {
		return err
	}
	if normalized != s.URL {
		return fmt.Errorf("source url %q is not normalized", s.URL)
	}
	if s.ID != SourceID(normalized) {
		return fmt.Errorf("source id %q does not match its url", s.ID)
	}
	if !utf8.ValidString(s.Title) {
		return fmt.Errorf("source title is not valid UTF-8")
	}
	if s.PublishedAt != "" && ParsePublishedAt(s.PublishedAt) != s.PublishedAt {
		return fmt.Errorf("source published_at %q is not canonical RFC 3339", s.PublishedAt)
	}
	return nil
}

// Validate checks one evidence item's closed fields.
func (e Evidence) Validate() error {
	if e.SourceID == "" {
		return fmt.Errorf("evidence source id is required")
	}
	switch e.Kind {
	case KindSnippet, KindExcerpt, KindDocument, KindSummary:
	default:
		return fmt.Errorf("unsupported evidence kind %q", e.Kind)
	}
	switch e.Format {
	case FormatText, FormatMarkdown:
	default:
		return fmt.Errorf("unsupported evidence format %q", e.Format)
	}
	switch e.Acquisition {
	case AcquisitionSearchService, AcquisitionHTTPFetch:
	default:
		return fmt.Errorf("unsupported evidence acquisition %q", e.Acquisition)
	}
	if !utf8.ValidString(e.Text) {
		return fmt.Errorf("evidence text is not valid UTF-8")
	}
	if e.RetrievedAt <= 0 {
		return fmt.Errorf("evidence retrieved_at must be positive")
	}
	if e.ReturnedBytes < 0 {
		return fmt.Errorf("evidence returned_bytes cannot be negative")
	}
	return nil
}
