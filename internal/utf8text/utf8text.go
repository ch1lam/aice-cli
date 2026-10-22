// Package utf8text bounds UTF-8 text by bytes without splitting a character.
package utf8text

import "unicode/utf8"

// Prefix returns the longest prefix of value that is at most limit bytes and
// ends on a rune boundary. Invalid bytes already in value are kept as-is.
func Prefix(value string, limit int) string {
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
