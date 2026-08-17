package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// resolveReadTarget finds a readable path for a read request. It applies
// tolerant, read-only normalization and fallbacks that keep the base
// resolution workspace-bound:
//
//  1. Normalize the raw input: strip a leading "@" prefix (some clients
//     emit "@file"), expand "~/", and fold the Unicode spaces commonly
//     produced by screen readers and copy/paste (NBSP, narrow NBSP,
//     figure space, thin spaces) to plain spaces.
//  2. Resolve against the workspace; absolute paths pass through.
//  3. If the resolved path does not exist, try the filename variants
//     macOS actually stores on disk: NFD decompositions for accented
//     letters ("café" typed as NFC while macOS keeps "cafe\u0301"),
//     U+2019 right single quotation marks in screenshot names, and the
//     narrow no-break space macOS inserts before AM/PM.
//
// If no variant exists the base resolved path is returned so the caller
// reports the original path in its error message.
func (r *Read) resolveReadTarget(input string) (string, error) {
	path, err := r.workspace.resolvePath(normalizeReadInput(input))
	if err != nil {
		return "", err
	}
	return findExistingVariant(path), nil
}

// normalizeReadInput applies input spelling fixes before path resolution.
func normalizeReadInput(input string) string {
	input = strings.TrimPrefix(input, "@")
	if home, ok := expandedHome(input); ok {
		input = home
	}
	return foldUnicodeSpaces(input)
}

// expandedHome resolves a leading "~" to the user's home directory.
func expandedHome(input string) (string, bool) {
	if input != "~" && !strings.HasPrefix(input, "~/") {
		return input, false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return input, false
	}
	if input == "~" {
		return home, true
	}
	return filepath.Join(home, strings.TrimPrefix(input, "~/")), true
}

// foldUnicodeSpaces maps the spacing characters that commonly appear in
// copied text to a plain space so filenames resolve as typed.
func foldUnicodeSpaces(value string) string {
	if !hasUnicodeSpace(value) {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if isUnicodeSpace(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasUnicodeSpace(value string) bool {
	for _, r := range value {
		if isUnicodeSpace(r) {
			return true
		}
	}
	return false
}

func isUnicodeSpace(r rune) bool {
	switch r {
	case '\u00A0', // no-break space
		'\u2007', // figure space
		'\u2009', // thin space
		'\u200A', // hair space
		'\u202F': // narrow no-break space
		return true
	}
	return false
}

// findExistingVariant returns the first existing macOS spelling variant of
// path, or path itself when none of the variants exist.
func findExistingVariant(path string) string {
	if fileExists(path) {
		return path
	}
	candidates := [...]string{
		macOSScreenshotVariant(path),
		nfdVariant(path),
		curlyApostropheVariant(path),
		curlyApostropheVariant(nfdVariant(path)),
	}
	for _, candidate := range candidates {
		if candidate != path && fileExists(candidate) {
			return candidate
		}
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// macOSScreenshotPattern matches the space before AM/PM that macOS renders
// as U+202F narrow no-break space in screenshot names such as
// "Screenshot 2024-01-02 at 3.42.10 AM.png".
var macOSScreenshotPattern = regexp.MustCompile(`(?i) (am|pm)\.`)

// macOSScreenshotVariant converts a typed plain space before AM/PM to the
// U+202F narrow no-break space macOS stores on disk.
func macOSScreenshotVariant(path string) string {
	return macOSScreenshotPattern.ReplaceAllString(path, "\u202F$1.")
}

// curlyApostropheVariant converts typed straight apostrophes to the U+2019
// right single quotation mark macOS uses in names like "Capture d'écran".
func curlyApostropheVariant(path string) string {
	return strings.ReplaceAll(path, "'", "\u2019")
}

// latinDecomposition is a subset of the UAX #15 canonical decompositions for
// the precomposed Latin letters that commonly appear in filenames (Latin-1
// Supplement plus the practical Latin Extended-A range). It lets read resolve
// NFC input against NFD names stored on macOS without pulling in a Unicode
// library. Characters without a decomposition (Â-like ligatures, Þ, ß, ...)
// are intentionally absent.
var latinDecomposition = map[rune]string{
	// Latin-1 Supplement (U+00C0–U+00FF, excluding × and ÷).
	'\u00C0': "A\u0300", '\u00C1': "A\u0301", '\u00C2': "A\u0302",
	'\u00C3': "A\u0303", '\u00C4': "A\u0308", '\u00C5': "A\u030A",
	'\u00C7': "C\u0327", '\u00C8': "E\u0300", '\u00C9': "E\u0301",
	'\u00CA': "E\u0302", '\u00CB': "E\u0308", '\u00CC': "I\u0300",
	'\u00CD': "I\u0301", '\u00CE': "I\u0302", '\u00CF': "I\u0308",
	'\u00D1': "N\u0303", '\u00D2': "O\u0300", '\u00D3': "O\u0301",
	'\u00D4': "O\u0302", '\u00D5': "O\u0303", '\u00D6': "O\u0308",
	'\u00D9': "U\u0300", '\u00DA': "U\u0301", '\u00DB': "U\u0302",
	'\u00DC': "U\u0308", '\u00DD': "Y\u0301", '\u00E0': "a\u0300",
	'\u00E1': "a\u0301", '\u00E2': "a\u0302", '\u00E3': "a\u0303",
	'\u00E4': "a\u0308", '\u00E5': "a\u030A", '\u00E7': "c\u0327",
	'\u00E8': "e\u0300", '\u00E9': "e\u0301", '\u00EA': "e\u0302",
	'\u00EB': "e\u0308", '\u00EC': "i\u0300", '\u00ED': "i\u0301",
	'\u00EE': "i\u0302", '\u00EF': "i\u0308", '\u00F1': "n\u0303",
	'\u00F2': "o\u0300", '\u00F3': "o\u0301", '\u00F4': "o\u0302",
	'\u00F5': "o\u0303", '\u00F6': "o\u0308", '\u00F9': "u\u0300",
	'\u00FA': "u\u0301", '\u00FB': "u\u0302", '\u00FC': "u\u0308",
	'\u00FD': "y\u0301", '\u00FF': "y\u0308",

	// Latin Extended-A: macron, breve, ogonek and hook variants.
	'\u0100': "A\u0304", '\u0101': "a\u0304", '\u0102': "A\u0306",
	'\u0103': "a\u0306", '\u0104': "A\u0328", '\u0105': "a\u0328",
	'\u0106': "C\u0301", '\u0107': "c\u0301", '\u0108': "C\u0302",
	'\u0109': "c\u0302", '\u010A': "C\u0307", '\u010B': "c\u0307",
	'\u010C': "C\u030C", '\u010D': "c\u030C", '\u010E': "D\u030C",
	'\u010F': "d\u030C", '\u0112': "E\u0304", '\u0113': "e\u0304",
	'\u0114': "E\u0306", '\u0115': "e\u0306", '\u0116': "E\u0307",
	'\u0117': "e\u0307", '\u0118': "E\u0328", '\u0119': "e\u0328",
	'\u011A': "E\u030C", '\u011B': "e\u030C", '\u011C': "G\u0302",
	'\u011D': "g\u0302", '\u011E': "G\u0306", '\u011F': "g\u0306",
	'\u0120': "G\u0307", '\u0121': "g\u0307", '\u0122': "G\u0327",
	'\u0123': "g\u0327", '\u0124': "H\u0302", '\u0125': "h\u0302",
	'\u0128': "I\u0303", '\u0129': "i\u0303", '\u012A': "I\u0304",
	'\u012B': "i\u0304", '\u012C': "I\u0306", '\u012D': "i\u0306",
	'\u012E': "I\u0328", '\u012F': "i\u0328", '\u0130': "I\u0307",
	'\u0134': "J\u0302", '\u0135': "j\u0302", '\u0136': "K\u0327",
	'\u0137': "k\u0327", '\u0139': "L\u0301", '\u013A': "l\u0301",
	'\u013B': "L\u0327", '\u013C': "l\u0327", '\u013D': "L\u030C",
	'\u013E': "l\u030C", '\u0143': "N\u0301", '\u0144': "n\u0301",
	'\u0145': "N\u0327", '\u0146': "n\u0327", '\u0147': "N\u030C",
	'\u0148': "n\u030C", '\u014C': "O\u0304", '\u014D': "o\u0304",
	'\u014E': "O\u0306", '\u014F': "o\u0306", '\u0150': "O\u030B",
	'\u0151': "o\u030B", '\u0154': "R\u0301", '\u0155': "r\u0301",
	'\u0156': "R\u0327", '\u0157': "r\u0327", '\u0158': "R\u030C",
	'\u0159': "r\u030C", '\u015A': "S\u0301", '\u015B': "s\u0301",
	'\u015C': "S\u0302", '\u015D': "s\u0302", '\u015E': "S\u0327",
	'\u015F': "s\u0327", '\u0160': "S\u030C", '\u0161': "s\u030C",
	'\u0164': "T\u030C", '\u0165': "t\u030C", '\u0168': "U\u0303",
	'\u0169': "u\u0303", '\u016A': "U\u0304", '\u016B': "u\u0304",
	'\u016C': "U\u0306", '\u016D': "u\u0306", '\u016E': "U\u030A",
	'\u016F': "u\u030A", '\u0170': "U\u030B", '\u0171': "u\u030B",
	'\u0172': "U\u0328", '\u0173': "u\u0328", '\u0174': "W\u0302",
	'\u0175': "w\u0302", '\u0176': "Y\u0302", '\u0177': "y\u0302",
	'\u0178': "Y\u0308", '\u0179': "Z\u0301", '\u017A': "z\u0301",
	'\u017B': "Z\u0307", '\u017C': "z\u0307", '\u017D': "Z\u030C",
	'\u017E': "z\u030C",

	// Latin Extended-B: widely-used caron forms (pinyin, Czech, Slovak).
	'\u01CD': "A\u030C", '\u01CE': "a\u030C", '\u01CF': "I\u030C",
	'\u01D0': "i\u030C", '\u01D1': "O\u030C", '\u01D2': "o\u030C",
	'\u01D3': "U\u030C", '\u01D4': "u\u030C", '\u01D5': "U\u0308\u0304",
	'\u01D6': "u\u0308\u0304", '\u01D7': "U\u0308\u0301",
	'\u01D8': "u\u0308\u0301", '\u01D9': "U\u0308\u030C",
	'\u01DA': "u\u0308\u030C", '\u01DB': "U\u0308\u0300",
	'\u01DC': "u\u0308\u0300", '\u01DE': "A\u0308\u0304",
	'\u01DF': "a\u0308\u0304", '\u01E0': "A\u0307\u0304",
	'\u01E1': "a\u0307\u0304", '\u01E2': "\u00C6\u0304",
	'\u01E3': "\u00E6\u0304", '\u01E6': "G\u030C", '\u01E7': "g\u030C",
	'\u01E8': "K\u030C", '\u01E9': "k\u030C", '\u01EA': "O\u0328",
	'\u01EB': "o\u0328", '\u01EC': "O\u0328\u0304",
	'\u01ED': "o\u0328\u0304", '\u01EE': "\u01B7\u030C",
	'\u01EF': "\u0292\u030C", '\u01F0': "j\u030C",
	'\u01F4': "G\u0301", '\u01F5': "g\u0301", '\u01F8': "N\u0300",
	'\u01F9': "n\u0300", '\u01FA': "A\u030A\u0301",
	'\u01FB': "a\u030A\u0301", '\u01FC': "\u00C6\u0301",
	'\u01FD': "\u00E6\u0301", '\u01FE': "\u00D8\u0301",
	'\u01FF': "\u00F8\u0301",
}

// nfdVariant decomposes precomposed Latin letters to the NFD form macOS
// stores on disk. It returns the input unchanged when no letter needs
// decomposing.
func nfdVariant(path string) string {
	var b strings.Builder
	changed := false
	for _, r := range path {
		if d, ok := latinDecomposition[r]; ok {
			b.WriteString(d)
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return path
	}
	return b.String()
}
