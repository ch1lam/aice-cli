package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// resolveReadTarget applies read-only spelling tolerance, then resolves relative
// paths against the workspace. Absolute paths and parent traversal remain valid;
// the Guard owns access checks on the requested and resolved physical targets.
// Missing targets retain the normalized base path for error reporting.
func (r *Read) resolveReadTarget(input string) (string, error) {
	path, err := r.workspace.resolvePath(normalizeReadInput(input))
	if err != nil {
		return "", err
	}
	return findExistingVariant(path, fileExists), nil
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
	return r == '\u00A0' || (r >= '\u2000' && r <= '\u200A') ||
		r == '\u202F' || r == '\u205F' || r == '\u3000'
}

// findExistingVariant returns the first existing macOS spelling variant of
// path, or path itself when none exists. Priority is normalized base, screenshot
// AM/PM space, NFD, curly apostrophe, then NFD + curly apostrophe. Do not retry
// lower-priority candidates after an open or permission failure on the winner.
// The probe is injected so tests can distinguish NFC/NFD even on macOS.
func findExistingVariant(path string, exists func(string) bool) string {
	if exists(path) {
		return path
	}
	candidates := [...]string{
		macOSScreenshotVariant(path),
		nfdVariant(path),
		curlyApostropheVariant(path),
		curlyApostropheVariant(nfdVariant(path)),
	}
	for _, candidate := range candidates {
		if candidate != path && exists(candidate) {
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

// nfdVariant applies canonical Unicode decomposition, including non-Latin
// scripts and canonical combining-mark order. Compatibility forms stay distinct.
func nfdVariant(path string) string {
	return norm.NFD.String(path)
}
