package interaction

import "strings"

// DetailTone classifies one detail value for display emphasis.
type DetailTone uint8

const (
	ToneDefault DetailTone = iota
	// ToneTitle marks the subject a group of details describes.
	ToneTitle
	// ToneReference marks identifiers, sources and locations.
	ToneReference
	ToneGood
	ToneWarning
	ToneBad
	ToneMuted
)

// Detail is one line of a structured detail view: a Heading, a Key/Value
// pair, or plain Text. A zero Detail is a blank line.
type Detail struct {
	Heading    string
	Key, Value string
	Text       string
	Tone       DetailTone
	Indent     int
}

// DetailsText renders details as plain text for text-only frontends.
func DetailsText(details []Detail) string {
	lines := make([]string, len(details))
	for i, detail := range details {
		line := detail.Heading + detail.Text
		if detail.Key != "" {
			line = detail.Key + ": " + detail.Value
		}
		lines[i] = strings.Repeat(" ", detail.Indent) + line
	}
	return strings.Join(lines, "\n")
}
