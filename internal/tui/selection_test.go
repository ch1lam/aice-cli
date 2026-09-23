package tui

import (
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTranscriptSelectionSelectedRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		selection transcriptSelection
		wantStart selectionPoint
		wantEnd   selectionPoint
		want      bool
	}{
		{
			name: "forward",
			selection: transcriptSelection{
				anchor: selectionPoint{item: 0, part: 0, line: 2, column: 3},
				focus:  selectionPoint{item: 0, part: 0, line: 4, column: 5},
				moved:  true,
			},
			wantStart: selectionPoint{item: 0, part: 0, line: 2, column: 3},
			wantEnd:   selectionPoint{item: 0, part: 0, line: 4, column: 6},
			want:      true,
		},
		{
			name: "reverse",
			selection: transcriptSelection{
				anchor: selectionPoint{item: 0, part: 0, line: 4, column: 5},
				focus:  selectionPoint{item: 0, part: 0, line: 2, column: 3},
				moved:  true,
			},
			wantStart: selectionPoint{item: 0, part: 0, line: 2, column: 3},
			wantEnd:   selectionPoint{item: 0, part: 0, line: 4, column: 6},
			want:      true,
		},
		{
			name: "cross item orders by item",
			selection: transcriptSelection{
				anchor: selectionPoint{item: 3, part: 0, line: 0, column: 0},
				focus:  selectionPoint{item: 1, part: 2, line: 5, column: 9},
				moved:  true,
			},
			wantStart: selectionPoint{item: 1, part: 2, line: 5, column: 9},
			wantEnd:   selectionPoint{item: 3, part: 0, line: 0, column: 1},
			want:      true,
		},
		{
			name: "no drag",
			selection: transcriptSelection{
				anchor: selectionPoint{item: 0, part: 0, line: 2, column: 3},
				focus:  selectionPoint{item: 0, part: 0, line: 2, column: 3},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotStart, gotEnd, got := tt.selection.selectedRange()
			if got != tt.want {
				t.Fatalf("selectedRange() selected = %t, want %t", got, tt.want)
			}
			if gotStart != tt.wantStart || gotEnd != tt.wantEnd {
				t.Errorf(
					"selectedRange() = (%+v, %+v), want (%+v, %+v)",
					gotStart,
					gotEnd,
					tt.wantStart,
					tt.wantEnd,
				)
			}
		})
	}
}

func TestSelectedTranscriptTextHandlesANSIAndMultipleLines(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 14})
	current.viewport.SetContent("alpha bravo\ncharlie delta")
	current.viewport.GotoTop()
	viewportTop := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))
	press := tea.Mouse{X: current.horizontalPadding() + 6, Y: viewportTop, Button: tea.MouseLeft}
	current = updateModel(t, current, tea.MouseClickMsg(press))
	drag := tea.Mouse{X: current.horizontalPadding() + 6, Y: viewportTop + 1, Button: tea.MouseLeft}
	current = updateModel(t, current, tea.MouseMotionMsg(drag))
	if got, want := selectedFrozenText(&current.selection.frozen, current.selection), "bravo\ncharlie"; got != want {
		t.Errorf("selected transcript text = %q, want %q", got, want)
	}
	highlighted := highlightCurrentWindow(&current.selection.frozen, current.selection)
	plain := ansi.Strip(current.selection.frozen.View())
	if ansi.Strip(highlighted) != plain {
		t.Errorf("highlight changed text content")
	}
	if highlighted == current.selection.frozen.View() {
		t.Errorf("highlight did not style the selection")
	}
	// The selected words must survive stripping and the highlight must not
	// be a no-op: both rows are styled in place.
	for _, selected := range []string{"bravo", "charlie"} {
		if !strings.Contains(plain, selected) || !strings.Contains(ansi.Strip(highlighted), selected) {
			t.Errorf("highlighted transcript lost %q", selected)
		}
	}
}

func TestModelMouseDragSelectsAndCopiesTranscript(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 14})
	current.viewport.SetContent(
		"alpha bravo\n" + strings.Repeat("line\n", 30),
	)
	current.viewport.GotoTop()
	viewportTop := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))

	current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{
		X:      current.horizontalPadding(),
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	if !current.selection.active {
		t.Fatal("mouse down did not start transcript selection")
	}

	current = updateModel(t, current, tea.MouseMotionMsg(tea.Mouse{
		X:      current.horizontalPadding() + 4,
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	if !current.selection.moved {
		t.Fatal("mouse drag did not extend transcript selection")
	}
	if view := current.View().Content; !strings.Contains(
		view,
		transcriptSelectionStyle.Render("alpha"),
	) {
		t.Fatalf("dragged transcript does not render selection highlight: %q", view)
	}

	current.viewport.SetContent(
		"replacement\n" + strings.Repeat("new line\n", 30),
	)
	current.viewport.GotoTop()
	if view := current.View().Content; !strings.Contains(
		view,
		transcriptSelectionStyle.Render("alpha"),
	) {
		t.Fatalf("content refresh changed the active selection snapshot: %q", view)
	}

	updated, command := current.Update(tea.MouseReleaseMsg(tea.Mouse{
		X:      current.horizontalPadding() + 4,
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	current, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model = %T, want tui.model", updated)
	}
	if command == nil {
		t.Fatal("mouse release did not return a clipboard command")
	}
	if got, want := fmt.Sprint(command().(tea.BatchMsg)[0]()), "alpha"; got != want {
		t.Errorf("clipboard content = %q, want %q", got, want)
	}
	if current.selection.active {
		t.Fatal("selection remains active after mouse release")
	}
	if !strings.Contains(ansi.Strip(current.View().Content), "✓ Copied") {
		t.Fatal("copy confirmation missing from view")
	}
	if current.status != "Ready" {
		t.Errorf("copy changed run status to %q", current.status)
	}

	initialOffset := current.viewport.YOffset()
	current = updateModel(t, current, tea.MouseWheelMsg(tea.Mouse{
		Button: tea.MouseWheelDown,
	}))
	if current.selection.moved {
		t.Fatal("mouse wheel did not clear the existing selection")
	}
	if got, want := current.viewport.YOffset(), initialOffset+current.viewport.MouseWheelDelta; got != want {
		t.Errorf("viewport Y offset = %d, want %d after mouse wheel", got, want)
	}
}

func TestCachedHighlightMatchesPureRecompute(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 100, Height: 30})
	var code strings.Builder
	code.WriteString("```go\n")
	for i := 0; i < 40; i++ {
		code.WriteString("func example(i int) int {\n\treturn i * 2\n}\n")
	}
	code.WriteString("```\n")
	current.entries = append(current.entries, transcriptEntry{
		kind:         entryAssistant,
		text:         "# Cached highlight\n\n" + code.String() + "\n" + strings.Repeat("trailing prose line\n", 10),
		complete:     true,
		presentation: &assistantPresentation{},
	})
	current.refreshViewport(true)
	viewportTop := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))
	press := tea.Mouse{X: current.horizontalPadding(), Y: viewportTop, Button: tea.MouseLeft}
	current = updateModel(t, current, tea.MouseClickMsg(press))

	assertCached := func(step string) {
		t.Helper()
		cached := current.selection.highlightedView()
		pure := highlightCurrentWindow(&current.selection.frozen, current.selection)
		if cached != pure {
			t.Fatalf("%s: cached highlight differs from pure recompute", step)
		}
	}
	assertCached("press without drag")

	height := current.viewport.Height()
	// Forward drag, then reverse back through the anchor, then forward again.
	positions := []int{1, height / 2, height - 1, height / 2, 0, 2, height - 1}
	for i, y := range positions {
		current = updateModel(t, current, tea.MouseMotionMsg(tea.Mouse{
			X:      current.horizontalPadding() + 20 + i,
			Y:      viewportTop + y,
			Button: tea.MouseLeft,
		}))
		assertCached(fmt.Sprintf("drag step %d", i))
	}

	// Returning to the anchor keeps moved latched, so both paths must agree
	// on the single collapsed cell.
	current = updateModel(t, current, tea.MouseMotionMsg(press))
	assertCached("back at anchor")
}

func TestStreamingBatchDuringSelectionDefersLayout(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 100, Height: 30})
	seed := strings.Repeat("streaming seed line with enough text to wrap. ", 60)
	current.entries = append(current.entries, transcriptEntry{
		kind:         entryAssistant,
		text:         seed,
		presentation: &assistantPresentation{},
	})
	current.assistantEntry = 0
	current.running = true
	current.refreshViewport(true)

	viewportTop := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))
	press := tea.Mouse{X: current.horizontalPadding(), Y: viewportTop, Button: tea.MouseLeft}
	current = updateModel(t, current, tea.MouseClickMsg(press))
	current = updateModel(t, current, tea.MouseMotionMsg(tea.Mouse{
		X:      current.horizontalPadding() + 10,
		Y:      viewportTop + 10,
		Button: tea.MouseLeft,
	}))
	if !current.selection.active || !current.selection.moved {
		t.Fatal("drag did not start a moved selection")
	}
	frozenHighlight := current.selection.highlightedView()
	frozenCopy := selectedFrozenText(
		&current.selection.frozen,
		current.selection,
	)
	liveFirstLine := strings.Split(current.viewport.View(), "\n")[0]

	// Streaming batches grow the transcript while the frozen snapshot owns
	// the screen. The frozen highlight and copy text must not change, and
	// the live viewport must stay pinned instead of following along with
	// layout work nobody can see.
	for i := 0; i < 3; i++ {
		updated, _ := current.applyRunBatch(runBatchMsg{updates: []runUpdate{{event: DisplayEvent{
			Kind:  DisplayEventAssistantDelta,
			Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: " streaming-token-0123456789"},
		}}}})
		current = updated.(model)
	}
	if !current.selection.active {
		t.Fatal("streaming batch ended the active selection")
	}
	if got := current.selection.highlightedView(); got != frozenHighlight {
		t.Fatal("streaming batch changed the frozen selection display")
	}
	if got := selectedFrozenText(
		&current.selection.frozen,
		current.selection,
	); got != frozenCopy {
		t.Fatal("streaming batch changed the frozen copy text")
	}
	if !strings.Contains(current.entries[0].text, "streaming-token") {
		t.Fatal("streaming batch did not reach the transcript entries")
	}
	if got := strings.Split(current.viewport.View(), "\n")[0]; got != liveFirstLine {
		t.Fatal("live viewport followed streaming content during a frozen selection")
	}

	// Release still copies the frozen text, and the next batch resumes
	// following to the new bottom.
	release := tea.MouseReleaseMsg(tea.Mouse{
		X:      current.horizontalPadding() + 10,
		Y:      viewportTop + 10,
		Button: tea.MouseLeft,
	})
	updated, command := current.Update(release)
	current = updated.(model)
	if command == nil {
		t.Fatal("release did not return a clipboard command")
	}
	if got := fmt.Sprint(command().(tea.BatchMsg)[0]()); got != frozenCopy {
		t.Errorf("released copy = %q, want frozen %q", got, frozenCopy)
	}
	updated, _ = current.applyRunBatch(runBatchMsg{updates: []runUpdate{{event: DisplayEvent{
		Kind:  DisplayEventAssistantDelta,
		Delta: DisplayDelta{Kind: DisplayDeltaText, Delta: " after-release-token"},
	}}}})
	current = updated.(model)
	// The token may wrap across visual rows, so flatten whitespace before
	// matching; following to the bottom is what matters here.
	stripped := ansi.Strip(current.viewport.View())
	flat := strings.ReplaceAll(strings.ReplaceAll(stripped, "\n", ""), " ", "")
	if !strings.Contains(flat, "after-release-token") {
		t.Fatal("viewport did not resume following after selection release")
	}
}

func TestModelMouseClickWithoutDragDoesNotCopy(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 14})
	current.viewport.SetContent("alpha")
	viewportTop := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))

	current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{
		X:      current.horizontalPadding() + 1,
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	updated, command := current.Update(tea.MouseReleaseMsg(tea.Mouse{
		X:      current.horizontalPadding() + 1,
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	current, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model = %T, want tui.model", updated)
	}
	if command != nil {
		t.Fatal("click without a drag returned a clipboard command")
	}
	if current.selection.active || current.selection.moved {
		t.Fatal("click without a drag left a visible selection")
	}
}

func TestCopyNoticePreservesActivityAndExpires(t *testing.T) {
	for _, state := range []string{"idle", "thinking", "side"} {
		t.Run(state, func(t *testing.T) {
			current := newModel(make(chan runRequest), make(chan struct{}))
			current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 14})
			current.running = state == "thinking"
			current.side.isVisible = state == "side"
			current.status = "Thinking..."
			current.viewport.SetContent("alpha")
			top := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))
			copySelection := func() {
				current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{X: current.horizontalPadding(), Y: top, Button: tea.MouseLeft}))
				updated, _ := current.Update(tea.MouseReleaseMsg(tea.Mouse{X: current.horizontalPadding() + 4, Y: top, Button: tea.MouseLeft}))
				current = updated.(model)
			}
			beforeHeight := lipgloss.Height(current.View().Content)
			copySelection()
			if current.status != "Thinking..." {
				t.Fatalf("copy replaced activity with %q", current.status)
			}
			old := copyNoticeExpiredMsg(current.copyGeneration)
			copySelection()
			current = updateModel(t, current, old)
			current.applyAgentEvent(DisplayEvent{Kind: DisplayEventAssistantStart})
			current.refreshViewport(false)
			if !strings.Contains(ansi.Strip(current.View().Content), "✓ Copied") {
				t.Fatal("copy notice lost after another copy or agent event")
			}
			if strings.Contains(current.transcriptView(), "✓ Copied") ||
				!strings.Contains(ansi.Strip(current.activityIndicator()), "Thinking...") {
				t.Fatal("copy notice replaced activity")
			}
			if lipgloss.Height(current.View().Content) != beforeHeight {
				t.Fatal("copy notice changed screen height")
			}
			current = updateModel(t, current, copyNoticeExpiredMsg(current.copyGeneration))
			if strings.Contains(ansi.Strip(current.View().Content), "✓ Copied") {
				t.Fatal("expired copy notice remains visible")
			}
		})
	}
}

func TestCopyBubbleKeepsFooterAndCursor(t *testing.T) {
	for _, width := range []int{24, 40, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			current := newModel(make(chan runRequest), make(chan struct{}))
			current = updateModel(t, current, tea.WindowSizeMsg{Width: width, Height: 14})
			before := current.View()
			footer := current.footerView(width)
			current.copyNotice = true
			after := current.View()
			if current.footerView(width) != footer || *after.Cursor != *before.Cursor {
				t.Fatal("bubble changed footer or cursor")
			}
			if lipgloss.Width(after.Content) != lipgloss.Width(before.Content) ||
				lipgloss.Height(after.Content) != lipgloss.Height(before.Content) {
				t.Fatal("bubble changed screen dimensions")
			}
			lines := strings.Split(ansi.Strip(after.Content), "\n")
			top := len(lines) - current.verticalPadding() - lipgloss.Height(current.footerView(current.layoutWidth())) -
				lipgloss.Height(current.composerView(current.layoutWidth())) - 3
			if !strings.Contains(lines[top], "╭") || !strings.Contains(lines[top+1], "✓ Copied") {
				t.Fatal("bubble missing above composer")
			}
			left := ansi.StringWidth(strings.Split(lines[top], "╭")[0])
			if left != (width-14)/2 {
				t.Fatalf("bubble left = %d, want centered", left)
			}
		})
	}
}

func TestCopyNoticeTimerExpiresAfterOneSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		current := newModel(make(chan runRequest), make(chan struct{}))
		current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 14})
		current.viewport.SetContent("alpha")
		top := current.verticalPadding() + lipgloss.Height(current.headerView(current.layoutWidth()))
		current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{X: current.horizontalPadding(), Y: top, Button: tea.MouseLeft}))
		updated, cmd := current.Update(tea.MouseReleaseMsg(tea.Mouse{X: current.horizontalPadding() + 4, Y: top, Button: tea.MouseLeft}))
		current = updated.(model)
		started := time.Now()
		expired := cmd().(tea.BatchMsg)[1]()
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Fatalf("notice duration = %s, want 1s", elapsed)
		}
		current = updateModel(t, current, expired)
		if current.copyNotice {
			t.Fatal("timer did not dismiss bubble")
		}
	})
}

func TestCopyNoticePreservesHeadingBackground(t *testing.T) {
	t.Parallel()
	for _, width := range []int{40, 80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			t.Parallel()
			current := newModel(make(chan runRequest), make(chan struct{}))
			current = updateModel(t, current, tea.WindowSizeMsg{Width: width, Height: 24})
			current.entries = []transcriptEntry{{kind: entryAssistant,
				text: "# AICE 项目介绍\n\n正文\n\n## 现在能做什么\n\n更多内容", complete: true}}
			current.refreshViewport(true)
			before := current.View()
			baseline := lipgloss.NewCanvas(width, current.height).Compose(lipgloss.NewLayer(before.Content))
			for _, state := range []string{"before", "copied", "expired"} {
				switch state {
				case "copied":
					current.copyText("正文")
				case "expired":
					current = updateModel(t, current, copyNoticeExpiredMsg(current.copyGeneration))
				}
				view := current.View()
				assertCanvasBackground(t, view.Content, nil)
				canvas := lipgloss.NewCanvas(width, current.height).Compose(lipgloss.NewLayer(view.Content))
				found := false
				for y, row := range strings.Split(view.Content, "\n") {
					if !strings.Contains(ansi.Strip(row), "AICE 项目介绍") {
						continue
					}
					found = true
					assertCanvasBackground(t, ansi.Cut(row, 30, width-2), inkBlackColor)
					for x := range width {
						if !baseline.CellAt(x, y).Equal(canvas.CellAt(x, y)) {
							t.Fatalf("%s changed heading cell (%d, %d)", state, x, y)
						}
					}
				}
				if !found {
					t.Fatal("heading missing")
				}
			}
		})
	}
}
