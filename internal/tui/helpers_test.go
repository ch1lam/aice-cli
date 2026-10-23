package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

// testModel is a sized model whose run controller nobody serves.
func testModel(t *testing.T, width, height int, commands ...interaction.Command) model {
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

// frontendOf offers every capability a test runner implements, as the
// application does for its Session.
func frontendOf(runner interaction.Runner) interaction.Frontend {
	frontend := interaction.Frontend{Runner: runner}
	frontend.State, _ = runner.(interaction.RuntimeStateProvider)
	frontend.Commands, _ = runner.(interaction.CommandRunner)
	frontend.SideThreads, _ = runner.(interaction.SideThreadManager)
	frontend.Sessions, _ = runner.(interaction.SessionBrowser)
	frontend.Usage, _ = runner.(interaction.UsageReader)
	frontend.Settings, _ = runner.(interaction.SettingsReader)
	frontend.SettingsStatus, _ = runner.(interaction.SettingsStatusReader)
	frontend.SettingsActions, _ = runner.(interaction.SettingsActionRunner)
	frontend.SettingsWriter, _ = runner.(interaction.SettingsWriter)
	frontend.Files, _ = runner.(interaction.FileCompleter)
	frontend.Guards, _ = runner.(interaction.GuardRequester)
	frontend.Questions, _ = runner.(interaction.QuestionRequester)
	return frontend
}
