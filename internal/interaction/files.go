package interaction

import (
	"strings"
	"unicode"
)

// FileReference is an explicit @ token. Offsets are rune indexes, allowing the
// editor and submission parser to share exactly the same quoting rules.
type FileReference struct {
	Path       string
	Start, End int
	Complete   bool
}

// ScanFileReferences recognizes whitespace-delimited @path and @"path with
// spaces" tokens outside backtick code. @@ and email addresses stay literal.
// The final incomplete token is returned for completion, never for submission.
func ScanFileReferences(text string) []FileReference {
	r := []rune(text)
	var refs []FileReference
	code := 0
	for i := 0; i < len(r); {
		if r[i] == '`' {
			j := i
			for j < len(r) && r[j] == '`' {
				j++
			}
			if code == 0 {
				code = j - i
			} else if code == j-i {
				code = 0
			}
			i = j
			continue
		}
		if code != 0 || r[i] != '@' || (i > 0 && !unicode.IsSpace(r[i-1])) {
			i++
			continue
		}
		start := i
		i++
		if i < len(r) && r[i] == '@' {
			for i < len(r) && !unicode.IsSpace(r[i]) {
				i++
			}
			continue
		}
		var path strings.Builder
		complete := true
		if i < len(r) && (r[i] == '"' || r[i] == '\'') {
			quote := r[i]
			i++
			complete = false
			for i < len(r) {
				if r[i] == quote {
					i++
					complete = true
					break
				}
				if r[i] == '\\' && i+1 < len(r) && (r[i+1] == quote || r[i+1] == '\\') {
					i++
				}
				path.WriteRune(r[i])
				i++
			}
			if i < len(r) && !unicode.IsSpace(r[i]) {
				complete = false
				for i < len(r) && !unicode.IsSpace(r[i]) {
					i++
				}
			}
		} else {
			for i < len(r) && !unicode.IsSpace(r[i]) {
				path.WriteRune(r[i])
				i++
			}
		}
		refs = append(refs, FileReference{Path: path.String(), Start: start, End: i, Complete: complete})
	}
	return refs
}

// FileReferences extracts only complete, nonempty references. Frontends call
// this on editable text before expanding opaque pasted-content placeholders.
func FileReferences(text string) []string {
	var paths []string
	for _, ref := range ScanFileReferences(text) {
		if ref.Complete && ref.Path != "" {
			paths = append(paths, ref.Path)
		}
	}
	return paths
}

// QuoteFileReference produces a token that round-trips even with spaces or quotes.
func QuoteFileReference(path string) string {
	return "@\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(path) + "\""
}
