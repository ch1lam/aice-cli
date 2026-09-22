package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Shared plain-text sanitization for terminal display. Terminal tab stops
// (8 cells) never match measured widths, a carriage return overwrites the
// current line, and an embedded ANSI reset clears the surrounding style
// mid-line, so all plain-text paths expand tabs the same way before
// measuring. Code panels are the exception: escapeCodeRow in code_block.go
// keeps its own visible escaping to preserve source fidelity and CRLF diffs.
const sanitizeTabExpansion = "    "

// sanitizeMultilineText keeps bodies (user bubbles, multiline tool details,
// preview content) as plain text: normalize line endings, expand tabs,
// strip ANSI, repair invalid UTF-8, and make remaining controls visible.
// Newlines are preserved; use sanitizeSingleLineText for titles and labels.
func sanitizeMultilineText(text string) string {
	text = strings.ToValidUTF8(text, "�")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", sanitizeTabExpansion)
	text = ansi.Strip(text)
	return strings.Map(func(character rune) rune {
		if character == '\n' {
			return character
		}
		if unicode.IsControl(character) {
			return '�'
		}
		return character
	}, text)
}

// sanitizeSingleLineText keeps titles, paths, labels and single-line
// details as plain text: strip ANSI, repair invalid UTF-8, and make every
// control (including newlines and tabs) visible. Callers that need no
// surrounding space trim explicitly, e.g. thread titles.
func sanitizeSingleLineText(text string) string {
	text = strings.ToValidUTF8(text, "�")
	text = ansi.Strip(text)
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return '�'
		}
		return character
	}, text)
}
