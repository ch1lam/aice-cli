package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func TestRenderDetailsSanitizesAndKeepsStructure(t *testing.T) {
	t.Parallel()
	view := ansi.Strip(renderDetails([]interaction.Detail{
		{Key: "State", Value: "ready\x1b[31m", Tone: interaction.ToneGood},
		{Heading: "Notes"},
		{Indent: 2, Text: "detail"},
	}, 40))
	if strings.Contains(view, "\x1b") || !strings.Contains(view, "State  ready") || !strings.Contains(view, "Notes") || !strings.Contains(view, "  detail") {
		t.Fatalf("rendered details = %q", view)
	}
}
