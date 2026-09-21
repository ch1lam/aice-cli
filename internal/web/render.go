package web

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/evidence"
)

// Model-facing output limits. These are AICE local bounds, not upstream limits.
const (
	MaxSearchOutputBytes     = 24 * 1024
	MaxFetchOutputBytes      = 32 * 1024
	MaxResultEvidenceBytes   = 2 * 1024
	searchUntrustedHeader    = "Search results (external, untrusted content):"
	searchFooter             = "Search excerpts are not proof that AICE fetched the complete page. Use web_fetch for source text when needed."
	fetchUntrustedHeader     = "Fetched content (external, untrusted content):"
	truncatedEvidenceMarker  = " […]"
	truncatedResultsNotice   = "[further results omitted: output limit]"
	truncatedDocumentMarker  = "\n[content truncated: output limit]"
	sanitizedPlaceholderRune = '\uFFFD'
)

// Sanitize removes terminal control sequences and format characters from
// untrusted text while preserving newlines and tabs. Invalid UTF-8 becomes the
// replacement rune so downstream JSON stays valid.
func Sanitize(value string) string {
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	// Strip complete escape sequences first so a removed ESC cannot leave its
	// parameters behind as visible text.
	value = ansi.Strip(strings.ToValidUTF8(value, string(sanitizedPlaceholderRune)))
	for _, r := range value {
		switch {
		case r == '\n' || r == '\t':
			builder.WriteRune(r)
		case r == '\r':
			continue
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			continue
		case unicode.Is(unicode.Cf, r) && r != '\u200d':
			// Bidi overrides and other format characters can disguise text in a
			// terminal; the zero-width joiner is kept for emoji sequences.
			continue
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// RenderSearch produces the deterministic model-facing text for one response.
// It contains no timestamps, request IDs or durations so identical results
// stay identical for repeated-tool detection.
func RenderSearch(response SearchResponse) string {
	var builder strings.Builder
	builder.WriteString(searchUntrustedHeader)
	builder.WriteString("\nQuery: ")
	builder.WriteString(Sanitize(response.Query))
	builder.WriteByte('\n')
	footer := "\n" + searchFooter
	budget := MaxSearchOutputBytes - len(footer) - len(truncatedResultsNotice) - 2
	if len(response.Evidence.Sources) == 0 {
		builder.WriteString("\nNo results.\n")
		builder.WriteString(footer)
		return builder.String()
	}
	for index, source := range response.Evidence.Sources {
		entry := renderSearchEntry(index+1, source, response.Evidence)
		if builder.Len()+len(entry) > budget {
			builder.WriteString("\n")
			builder.WriteString(truncatedResultsNotice)
			builder.WriteByte('\n')
			break
		}
		builder.WriteString(entry)
	}
	builder.WriteString(footer)
	return builder.String()
}

func renderSearchEntry(number int, source evidence.Source, bundle evidence.Bundle) string {
	var entry strings.Builder
	title := Sanitize(source.Title)
	if strings.TrimSpace(title) == "" {
		title = "(untitled)"
	}
	fmt.Fprintf(&entry, "\n[%d] %s\nURL: %s\n", number, strings.Join(strings.Fields(title), " "), Sanitize(source.URL))
	if source.PublishedAt != "" {
		fmt.Fprintf(&entry, "Published: %s\n", source.PublishedAt)
	}
	for _, item := range bundle.Items {
		if item.SourceID != source.ID {
			continue
		}
		text := Sanitize(strings.TrimSpace(item.Text))
		if text == "" {
			continue
		}
		if len(text) > MaxResultEvidenceBytes {
			text = truncateUTF8(text, MaxResultEvidenceBytes-len(truncatedEvidenceMarker)) + truncatedEvidenceMarker
		} else if item.Truncated {
			text += truncatedEvidenceMarker
		}
		fmt.Fprintf(&entry, "Evidence (%s): %s\n", item.Kind, text)
	}
	return entry.String()
}

// RenderFetch produces the deterministic model-facing text for one fetch.
func RenderFetch(response FetchResponse) string {
	var builder strings.Builder
	builder.WriteString(fetchUntrustedHeader)
	fmt.Fprintf(&builder, "\nURL: %s\n", Sanitize(response.RequestedURL))
	if response.FinalURL != "" && response.FinalURL != response.RequestedURL {
		fmt.Fprintf(&builder, "Final URL: %s\n", Sanitize(response.FinalURL))
	}
	fmt.Fprintf(&builder, "Content type: %s\n", Sanitize(response.MediaType))
	if response.Extraction != "" {
		fmt.Fprintf(&builder, "Extraction: %s\n", Sanitize(response.Extraction))
	}
	for _, source := range response.Evidence.Sources {
		if title := strings.TrimSpace(Sanitize(source.Title)); title != "" {
			fmt.Fprintf(&builder, "Title: %s\n", strings.Join(strings.Fields(title), " "))
		}
		break
	}
	builder.WriteString("\n")
	remaining := MaxFetchOutputBytes - builder.Len() - len(truncatedDocumentMarker) - 1
	truncated := false
	for _, item := range response.Evidence.Items {
		text := Sanitize(item.Text)
		if len(text) > remaining {
			text = truncateUTF8(text, max(remaining, 0))
			truncated = true
		}
		builder.WriteString(text)
		remaining -= len(text)
		if item.Truncated {
			truncated = true
		}
		if truncated {
			break
		}
	}
	if truncated {
		builder.WriteString(truncatedDocumentMarker)
	}
	builder.WriteByte('\n')
	return builder.String()
}

// truncateUTF8 cuts at a rune boundary without splitting a character.
func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// TruncateUTF8 is the exported form for adapters bounding evidence text.
func TruncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	return truncateUTF8(value, limit), true
}
