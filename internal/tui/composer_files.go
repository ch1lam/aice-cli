package tui

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// composerInput owns file-reference spans alongside the editable textarea.
// Positions distinguish an attached path from identical text typed elsewhere.
// Replacing a draft clears its spans; ordinary edits rebase surviving spans.
type composerInput struct {
	textarea.Model
	files []composerFile
}

type composerFile struct {
	start, end int // rune offsets in Value
	path       string
	editing    bool // Right-expanded references remain plain, editable text.
}

func (m *composerInput) SetValue(value string) {
	m.Model.SetValue(value)
	m.files = nil
}

func (m *composerInput) Reset() {
	m.Model.Reset()
	m.files = nil
}

func (m composerInput) cursorOffset() int {
	start := 0
	for _, row := range strings.Split(m.Value(), "\n")[:m.Line()] {
		start += utf8.RuneCountInString(row) + 1
	}
	return start + m.Column()
}

func (m composerInput) Update(message tea.Msg) (composerInput, tea.Cmd) {
	before, cursor := m.Value(), m.cursorOffset()
	var command tea.Cmd
	m.Model, command = m.Model.Update(message)
	m.rebaseFiles(before, cursor)
	return m, command
}

func (m *composerInput) InsertString(value string) {
	before, cursor := m.Value(), m.cursorOffset()
	m.Model.InsertString(value)
	m.rebaseFiles(before, cursor)
}

func (m *composerInput) rebaseFiles(before string, cursor int) {
	if len(m.files) == 0 || before == m.Value() {
		return
	}
	previous, next := []rune(before), []rune(m.Value())
	start := 0
	// Anchor at the editing cursor when repeated text makes the diff ambiguous.
	for start < min(len(previous), len(next), cursor) && previous[start] == next[start] {
		start++
	}
	oldEnd, newEnd := len(previous), len(next)
	for oldEnd > start && newEnd > start && previous[oldEnd-1] == next[newEnd-1] {
		oldEnd--
		newEnd--
	}
	kept := make([]composerFile, 0, len(m.files))
	for _, file := range m.files {
		switch {
		case file.editing && start > file.start && start <= file.end && oldEnd <= file.end:
			end := file.end + newEnd - oldEnd
			// A delimiter typed after the path ends the reference; whitespace
			// already inside a selected path remains part of its name.
			if start == file.end {
				for i := start; i < newEnd; i++ {
					if unicode.IsSpace(next[i]) {
						end = i
						break
					}
				}
			}
			file.end = end
			file.path = string(next[file.start+1 : end])
			if !strings.ContainsAny(file.path, "\r\n") {
				kept = append(kept, file)
			}
		case file.end <= start:
			kept = append(kept, file)
		case file.start >= oldEnd:
			file.start += newEnd - oldEnd
			file.end += newEnd - oldEnd
			kept = append(kept, file)
		}
	}
	m.files = kept
}

func (m *composerInput) replaceReference(start, end int, replacement string, path string, editing bool) {
	before := m.Value()
	runes := []rune(before)
	// Re-expanding the same path still replaces its editing state, even when
	// the visible text does not change.
	m.files = slices.DeleteFunc(slices.Clone(m.files), func(file composerFile) bool {
		return file.start == start
	})
	m.Model.SetValue(string(runes[:start]) + replacement + string(runes[end:]))
	m.rebaseFiles(before, start)
	if path != "" {
		m.files = append(slices.Clone(m.files), composerFile{
			start: start, end: start + utf8.RuneCountInString(fileReferenceLabel(path)), path: path, editing: editing,
		})
		slices.SortFunc(m.files, func(a, b composerFile) int { return a.start - b.start })
	}
}

func fileReferenceLabel(path string) string {
	return "@" + sanitizeSingleLineText(path)
}

// referenceText serializes known spans using the shared quoting syntax.
// UI labels remain unquoted even for spaces, quotes and literal @ characters.
func (m composerInput) referenceText() string {
	runes := []rune(m.Value())
	var text strings.Builder
	end := 0
	for _, file := range m.files {
		text.WriteString(string(runes[end:file.start]))
		text.WriteString(interaction.QuoteFileReference(file.path))
		end = file.end
	}
	text.WriteString(string(runes[end:]))
	return text.String()
}

// Mask confirmed spans before scanning editable text: a space or @ in a file
// label must not create extra references or interfere with the active query.
func (m composerInput) editableReferenceText() string {
	runes := []rune(m.Value())
	for _, file := range m.files {
		for i := file.start; i < file.end; i++ {
			runes[i] = ' '
		}
	}
	return string(runes)
}

func (m model) composerFiles() []string {
	refs := interaction.ScanFileReferences(m.input.editableReferenceText())
	for _, file := range m.input.files {
		refs = append(refs, interaction.FileReference{Path: file.path, Start: file.start, End: file.end, Complete: true})
	}
	slices.SortFunc(refs, func(a, b interaction.FileReference) int { return a.Start - b.Start })
	var paths []string
	for _, ref := range refs {
		if ref.Complete && ref.Path != "" {
			paths = append(paths, ref.Path)
		}
	}
	return paths
}

func (m composerInput) fileSpansInRow(row int) [][2]int {
	rows := strings.Split(m.Value(), "\n")
	if row < 0 || row >= len(rows) {
		return nil
	}
	start := 0
	for _, line := range rows[:row] {
		start += utf8.RuneCountInString(line) + 1
	}
	var spans [][2]int
	for _, file := range m.files {
		if !file.editing && file.start >= start && file.end <= start+utf8.RuneCountInString(rows[row]) {
			spans = append(spans, [2]int{file.start - start, file.end - start})
		}
	}
	return spans
}
