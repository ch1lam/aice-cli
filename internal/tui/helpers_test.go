package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// testModel is a sized model whose run controller nobody serves.
func testModel(t *testing.T, width, height int, commands ...SlashCommand) model {
	t.Helper()
	m := newModel(make(chan runRequest), make(chan struct{}), commands...)
	return updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
}

func updateModel(t *testing.T, current model, message tea.Msg) model {
	t.Helper()
	updated, _ := current.Update(message)
	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model = %T, want tui.model", updated)
	}
	return result
}

// findPainted locates the first cell where View paints text. Tests hit what
// is displayed instead of inventing rectangles.
func findPainted(t *testing.T, m model, text string) tea.Mouse {
	t.Helper()
	view := ansi.Strip(m.View().Content)
	for y, row := range strings.Split(view, "\n") {
		if x := strings.Index(row, text); x >= 0 {
			return tea.Mouse{X: ansi.StringWidth(row[:x]), Y: y, Button: tea.MouseLeft}
		}
	}
	t.Fatalf("%q is not painted in:\n%s", text, view)
	return tea.Mouse{}
}

// transcriptView materializes the whole transcript as one explicit snapshot.
// Live frames use lazy items; tests use this to assert on rendered history.
func (m model) transcriptView() string {
	var result strings.Builder
	for i, item := range m.transcriptItems() {
		if i > 0 {
			result.WriteString(strings.Repeat("\n", item.gap+1))
		}
		result.WriteString(item.content().view)
	}
	return result.String()
}
