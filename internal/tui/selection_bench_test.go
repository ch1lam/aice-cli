package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Static large-range selection benchmarks: the transcript is finished (no
// streaming), the viewport shows ANSI-heavy rows (markdown code fence with a
// line-number gutter plus expanded tool output), and the selection spans the
// whole viewport. Each drag-frame stage is measured separately so a later
// optimization can be attributed to the right stage.
func largeSelectionFixture(b *testing.B) model {
	b.Helper()

	current := newModel(nil, nil)
	updated, _ := current.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	current = updated.(model)

	var code strings.Builder
	code.WriteString("```go\n")
	for i := 0; i < 120; i++ {
		code.WriteString("func fooBarBazQux(i int, s string) (int, error) {\n\tif i < 0 {\n\t\treturn 0, errNegativeValue\n\t}\n\tfmt.Println(\"hello world\", i, s)\n\treturn i * 2, nil\n}\n")
	}
	code.WriteString("```\n\n")
	code.WriteString(strings.Repeat("Paragraph text with **bold** and `inline code` filling the viewport width. ", 40))

	for i := 0; i < 3; i++ {
		current.entries = append(current.entries, transcriptEntry{
			kind:       entryTool,
			toolID:     "tool-bench",
			toolName:   "bash",
			toolDetail: "go test ./internal/tui/ -run TestSelection -count=1",
			toolDone:   true,
			toolOutput: interaction.ToolOutputDisplay{Available: true, Text: strings.Repeat("ok  \tgithub.com/ch1lam/aice-cli/internal/tui\t1.593s\n", 60)},
		})
		current.setFoldExpanded(foldTarget{kind: foldTool, id: len(current.entries) - 1}, true)
	}
	current.entries = append(current.entries, transcriptEntry{
		kind:         entryAssistant,
		text:         "# Bench transcript\n\n" + code.String(),
		complete:     true,
		presentation: &assistantPresentation{},
	})
	current.refreshViewport(true)
	return current
}

// pressLargeSelection starts a drag at the top-left of the transcript and
// extends it to the bottom-right, covering the whole viewport.
func pressLargeSelection(m model) (model, tea.Mouse) {
	top := m.verticalPadding() + lipgloss.Height(m.headerView(m.layoutWidth()))
	press := tea.Mouse{X: m.horizontalPadding(), Y: top, Button: tea.MouseLeft}
	updated, _ := m.Update(tea.MouseClickMsg(press))
	m = updated.(model)
	drag := tea.Mouse{
		X:      m.horizontalPadding() + m.viewport.Width() - 1,
		Y:      top + m.viewport.Height() - 1,
		Button: tea.MouseLeft,
	}
	updated, _ = m.Update(tea.MouseMotionMsg(drag))
	return updated.(model), drag
}

func BenchmarkHighlightTranscriptSelection(b *testing.B) {
	m := largeSelectionFixture(b)
	m, _ = pressLargeSelection(m)
	view := m.selection.viewportView
	offset := m.selection.viewportOffset
	base := m.selection.focus
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		selection := m.selection
		selection.focus = transcriptPosition{row: base.row - (i % m.viewport.Height()), column: base.column}
		_ = highlightTranscriptSelection(view, selection, offset)
	}
}

func BenchmarkSelectionMouseMotionUpdate(b *testing.B) {
	m := largeSelectionFixture(b)
	m, _ = pressLargeSelection(m)
	top := m.verticalPadding() + lipgloss.Height(m.headerView(m.layoutWidth()))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		next := m
		motion := tea.MouseMotionMsg(tea.Mouse{
			X:      m.horizontalPadding() + (i % m.viewport.Width()),
			Y:      top + (i % m.viewport.Height()),
			Button: tea.MouseLeft,
		})
		updated, _ := next.Update(motion)
		_ = updated.(model)
	}
}

func BenchmarkSelectionView(b *testing.B) {
	m := largeSelectionFixture(b)
	m, _ = pressLargeSelection(m)
	// Warm the gesture highlight cache so the loop measures steady-state
	// drag frames, not the one-time frozen-geometry build.
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		next := m
		next.selection.update(transcriptPosition{
			row:    m.selection.viewportOffset + (i % m.viewport.Height()),
			column: 10 + (i % 40),
		})
		_ = next.View()
	}
}

func BenchmarkSelectionRelease(b *testing.B) {
	m := largeSelectionFixture(b)
	m, drag := pressLargeSelection(m)
	release := tea.MouseReleaseMsg(drag)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		next := m
		updated, _ := next.Update(release)
		_ = updated.(model)
	}
}
