package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func BenchmarkSelectionCopyCode(b *testing.B) {
	for _, lines := range []int{1000, 10000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			source := strings.Repeat("\tvalue := 42  \n", lines)
			content := newCodeBlock(source, "text").layout(codeBlockOptions{width: 80}).content()
			view := newTranscriptViewport()
			view.SetWidth(80)
			view.SetHeight(30)
			view.setItems([]transcriptItem{{renderContent: func() transcriptContent { return content }}})
			rows := view.partLines(0, 0)
			selection := transcriptSelection{
				anchor: selectionPoint{line: 1},
				focus:  selectionPoint{line: len(rows) - 2, column: 79},
				moved:  true,
			}
			if got := selectedFrozenText(&view, selection); got != strings.TrimSuffix(source, "\n") {
				b.Fatal("selection did not copy the complete source")
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = selectedFrozenText(&view, selection)
			}
		})
	}
}

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
	base := m.selection.focus
	frozen := m.selection.frozen
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		selection := m.selection
		// Move within the same window so the cached highlight path is
		// measured, not a full rebuild.
		rows := frozen.visibleRows()
		if len(rows) == 0 {
			b.Fatal("no frozen rows")
		}
		pick := rows[i%len(rows)]
		selection.focus = selectionPoint{item: pick.item, part: pick.part, line: pick.line, column: base.column}
		_ = highlightCurrentWindow(&frozen, selection)
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
	rows := m.selection.frozen.visibleRows()
	if len(rows) == 0 {
		b.Fatal("no frozen rows")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		next := m
		pick := rows[i%len(rows)]
		next.selection.update(selectionPoint{
			item: pick.item, part: pick.part, line: pick.line,
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

// Many-screen fixtures: the same ANSI-heavy content but the selection spans
// several viewport heights via wheels. Drag frames must stay O(window), only
// the release walks the full accumulated range.

func manyScreensSelection(b *testing.B, wheels int) (model, tea.Mouse) {
	b.Helper()
	m := largeSelectionFixture(b)
	m.viewport.GotoTop()
	top := m.verticalPadding() + lipgloss.Height(m.headerView(m.layoutWidth()))
	press := tea.Mouse{X: m.horizontalPadding(), Y: top, Button: tea.MouseLeft}
	updated, _ := m.Update(tea.MouseClickMsg(press))
	m = updated.(model)
	// Extend one screen first so moved latches, then wheel to accumulate.
	drag := tea.Mouse{X: m.horizontalPadding() + m.viewport.Width() - 1, Y: top + m.viewport.Height() - 1, Button: tea.MouseLeft}
	updated, _ = m.Update(tea.MouseMotionMsg(drag))
	m = updated.(model)
	for i := 0; i < wheels; i++ {
		wheel := tea.MouseWheelMsg(tea.Mouse{X: drag.X, Y: drag.Y, Button: tea.MouseWheelDown})
		updated, _ := m.Update(wheel)
		m = updated.(model)
	}
	// Final mouse position stays at the bottom of the current window.
	return m, drag
}

func BenchmarkSelectionSameWindowMoveManyScreens(b *testing.B) {
	m, _ := manyScreensSelection(b, 30)
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

func BenchmarkSelectionWheelWindowChange(b *testing.B) {
	m, drag := manyScreensSelection(b, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		button := tea.MouseWheelDown
		if i%2 == 1 {
			// Alternate direction to stay within content and avoid clamping
			// to a single edge for the whole run.
			button = tea.MouseWheelUp
		}
		wheel := tea.MouseWheelMsg(tea.Mouse{X: drag.X, Y: drag.Y, Button: button})
		updated, _ := m.Update(wheel)
		m = updated.(model)
	}
}

func BenchmarkSelectionReleaseManyScreens(b *testing.B) {
	m, drag := manyScreensSelection(b, 30)
	release := tea.MouseReleaseMsg(drag)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		next := m
		updated, _ := next.Update(release)
		_ = updated.(model)
	}
}
