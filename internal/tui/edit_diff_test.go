package tui

import (
	"strings"
	"testing"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func TestCompletedEditDiffInTranscript(t *testing.T) {
	for _, tt := range []struct {
		name   string
		failed bool
		diff   interaction.DiffDisplay
		want   string
	}{
		{name: "success", diff: interaction.DiffDisplay{Text: "@@ -1 +1 @@\n-old\n+new\n"}, want: "+new"},
		{name: "legacy"},
		{name: "failure", failed: true, diff: interaction.DiffDisplay{Text: "+not written\n"}},
		{name: "omitted", diff: interaction.DiffDisplay{Truncated: true}, want: "diff incomplete"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(nil, nil)
			m.width, m.height = 100, 30
			m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolStart, Tool: ToolDisplay{ID: "edit-1", Name: "edit", Detail: "missing-file"}})
			m.refreshViewport(true)
			if strings.Contains(m.transcriptView(), "@@") {
				t.Fatal("premature diff")
			}
			m.applyAgentEvent(DisplayEvent{Kind: DisplayEventToolEnd, Tool: ToolDisplay{ID: "edit-1", Failed: tt.failed, Diff: tt.diff}})
			m.expandAllDetails(true)
			m.refreshViewport(true)
			view := ansi.Strip(m.transcriptView())
			if tt.want != "" && !strings.Contains(view, tt.want) {
				t.Fatal(view)
			}
			if tt.want == "" && (strings.Contains(view, "not written") || strings.Contains(view, "@@")) {
				t.Fatal(view)
			}
			if tt.failed && !strings.Contains(view, "✕") {
				t.Fatal(view)
			}
		})
	}
}

func TestEditDiffDisplayLimitsAndSafety(t *testing.T) {
	diff := interaction.DiffDisplay{Text: "@@ -1 +1 @@\n-old\r\n+\x1b]52;c;evil\a\u202e\\r\t\n" + strings.Repeat(" context\n", 20)}
	view := ansi.Strip(editDiffView(diff, 100, false))
	for _, want := range []string{`-old\r`, `\x1b]52;c;evil\a\u202e\\r\t`, "ctrl+o expand"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q: %s", want, view)
		}
	}
	for _, control := range []string{"\x1b", "\a", "\r", "\u202e"} {
		if strings.Contains(view, control) {
			t.Fatalf("unsafe %q", view)
		}
	}
	full := ansi.Strip(editDiffView(diff, 100, true))
	if strings.Contains(full, "ctrl+o") || strings.Count(full, "context") != 20 {
		t.Fatal(full)
	}
	long := ansi.Strip(editDiffView(interaction.DiffDisplay{Text: "+" + strings.Repeat("界", 30000)}, 24, true))
	if !strings.Contains(long, "long lines clipped") || !strings.Contains(long, "diff incomplete") {
		t.Fatal(long)
	}
}

func TestEditDiffExpandedReplayStillBoundsRows(t *testing.T) {
	view := ansi.Strip(editDiffView(interaction.DiffDisplay{Text: strings.Repeat("+row\n", 3000)}, 80, true))
	if strings.Count(view, "+row") != 2000 || !strings.Contains(view, "diff incomplete") || strings.Contains(view, "ctrl+o expand") {
		t.Fatal("expanded replay limit was not reported accurately")
	}
}
