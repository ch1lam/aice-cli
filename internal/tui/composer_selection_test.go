package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestComposerSelectionKeysPreserveAtomicFileReference(t *testing.T) {
	t.Parallel()
	for _, mod := range []tea.KeyMod{tea.ModShift, tea.ModCtrl | tea.ModShift, tea.ModAlt | tea.ModShift} {
		t.Run(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: mod}.String(), func(t *testing.T) {
			m := attachTestFile(t, completionTestModel(), "main.go")
			m.input.SetCursorColumn(len("@main.go"))
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: mod})
			m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
			if m.input.Value() != "@main.gox " || len(m.input.files) != 1 {
				t.Fatalf("selection split attached file: %q, files=%v", m.input.Value(), m.input.files)
			}
		})
	}
}
