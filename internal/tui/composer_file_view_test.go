package tui

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestComposerFileViewColorsVisibleCellsWithoutMovingEditor(t *testing.T) {
	t.Parallel()
	type colorSpan struct{ row, start, end int }
	for _, tc := range []struct {
		name, value   string
		width, height int
		paths         []string
		markers, gold []colorSpan
	}{
		{"exact width", "@abc", 4, 2, []string{"abc"}, []colorSpan{{0, 0, 1}}, []colorSpan{{0, 1, 4}}},
		{"wrapped", "@abcdef", 5, 3, []string{"abcdef"}, []colorSpan{{0, 0, 1}}, []colorSpan{{0, 1, 5}, {1, 0, 2}}},
		{"wide", "@中文 x", 6, 3, []string{"中文"}, []colorSpan{{0, 0, 1}}, []colorSpan{{0, 1, 5}}},
		{"two files", "p @a @b z", 12, 3, []string{"a", "b"}, []colorSpan{{0, 2, 3}, {0, 5, 6}}, []colorSpan{{0, 3, 4}, {0, 6, 7}}},
		{"blank rows", "prefix\n\n@abc\ntrailing", 10, 6, []string{"abc"}, []colorSpan{{2, 0, 1}}, []colorSpan{{2, 1, 4}}},
		{"scrolled", "one\ntwo\nthree\n@abc", 10, 2, []string{"abc"}, []colorSpan{{1, 0, 1}}, []colorSpan{{1, 1, 4}}},
		{"emoji prefix", "👍🏽 @abc  ", 20, 3, []string{"abc"}, []colorSpan{{0, 3, 4}}, []colorSpan{{0, 4, 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, focused := range []bool{true, false} {
				m := newModel(nil, nil)
				m.input.DynamicHeight = false
				m.input.SetWidth(tc.width)
				m.input.SetHeight(tc.height)
				m.input.SetValue(tc.value)
				for _, path := range tc.paths {
					label := fileReferenceLabel(path)
					start := utf8.RuneCountInString(tc.value[:strings.Index(tc.value, label)])
					m.input.files = append(m.input.files, composerFile{start: start, end: start + utf8.RuneCountInString(label), path: path})
				}
				m.input.MoveToEnd()
				m.input, _ = m.input.Update(nil)
				if !focused {
					m.input.Blur()
				}
				row, col, scroll, cursor := m.input.Line(), m.input.Column(), m.input.ScrollYOffset(), m.input.Cursor()
				plain := m.input.Model.View()
				painted := m.input.View()
				if ansi.Strip(plain) != ansi.Strip(painted) || m.input.Value() != tc.value {
					t.Fatal("styling changed the draft or visible text")
				}
				if row != m.input.Line() || col != m.input.Column() || scroll != m.input.ScrollYOffset() ||
					focused != m.input.Focused() || !reflect.DeepEqual(cursor, m.input.Cursor()) {
					t.Fatal("styling moved the cursor, viewport or focus")
				}
				before := lipgloss.NewCanvas(tc.width, tc.height).Compose(lipgloss.NewLayer(plain))
				after := lipgloss.NewCanvas(tc.width, tc.height).Compose(lipgloss.NewLayer(painted))
				for y := range tc.height {
					for x := range tc.width {
						want := *before.CellAt(x, y)
						for _, span := range tc.markers {
							if want.Content != "" && span.row == y && x >= span.start && x < span.end {
								want.Style.Fg = mutedTextColor
							}
						}
						for _, span := range tc.gold {
							if want.Content != "" && span.row == y && x >= span.start && x < span.end {
								want.Style.Fg = secondaryColor
							}
						}
						if !want.Equal(after.CellAt(x, y)) {
							t.Fatalf("unexpected painted cell (%d,%d), focused=%t", x, y, focused)
						}
					}
				}
			}
		})
	}
}
