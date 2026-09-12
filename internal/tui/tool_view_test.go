package tui

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestToolBodyShowsOutputLimitsAndEscapesControls(t *testing.T) {
	m := newModel(nil, nil)
	m.width = 80
	entry := transcriptEntry{toolDone: true, toolOutput: interaction.ToolOutputDisplay{
		Available: true, Text: "\x1b]52;c;evil\a\u202e\n" + strings.Repeat("row\n", 2100),
	}}
	view := ansi.Strip(m.toolBodyView(entry))
	if strings.ContainsAny(view, "\x1b\a\u202e") || !strings.Contains(view, `\x1b]52`) {
		t.Fatal("output controls were not escaped")
	}
	if strings.Count(view, "row\n") > 1999 || !strings.Contains(view, "output display limit reached") {
		t.Fatal("missing output bounds")
	}
	entry.toolOutput = interaction.ToolOutputDisplay{Available: true}
	if !strings.Contains(m.toolBodyView(entry), "empty output") {
		t.Fatal("empty output not distinguished")
	}
	entry.toolOutput.Available = false
	if !strings.Contains(m.toolBodyView(entry), "output unavailable") {
		t.Fatal("missing output not distinguished")
	}
}
