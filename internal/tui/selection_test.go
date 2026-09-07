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
		wantStart transcriptPosition
		wantEnd   transcriptPosition
		want      bool
	}{
		{
			name: "forward",
			selection: transcriptSelection{
				anchor: transcriptPosition{row: 2, column: 3},
				focus:  transcriptPosition{row: 4, column: 5},
				moved:  true,
			},
			wantStart: transcriptPosition{row: 2, column: 3},
			wantEnd:   transcriptPosition{row: 4, column: 6},
			want:      true,
		},
		{
			name: "reverse",
			selection: transcriptSelection{
				anchor: transcriptPosition{row: 4, column: 5},
				focus:  transcriptPosition{row: 2, column: 3},
				moved:  true,
			},
			wantStart: transcriptPosition{row: 2, column: 3},
			wantEnd:   transcriptPosition{row: 4, column: 6},
			want:      true,
		},
		{
			name: "no drag",
			selection: transcriptSelection{
				anchor: transcriptPosition{row: 2, column: 3},
				focus:  transcriptPosition{row: 2, column: 3},
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

	firstLine := bodyStyle.Render("alpha bravo")
	secondLine := infoStyle.Render("charlie delta")
	view := firstLine + "\n" + secondLine
	selection := transcriptSelection{
		anchor:         transcriptPosition{row: 10, column: 6},
		focus:          transcriptPosition{row: 11, column: 6},
		viewportOffset: 10,
		moved:          true,
	}

	if got, want := selectedTranscriptText(view, selection, 10), "bravo\ncharlie"; got != want {
		t.Errorf("selected transcript text = %q, want %q", got, want)
	}

	highlighted := highlightTranscriptSelection(view, selection, 10)
	for _, selected := range []string{"bravo", "charlie"} {
		if !strings.Contains(
			highlighted,
			transcriptSelectionStyle.Render(selected),
		) {
			t.Errorf("highlighted transcript does not style %q: %q", selected, highlighted)
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
	viewportTop := lipgloss.Height(current.headerView(current.width))

	current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{
		X:      0,
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	if !current.selection.active {
		t.Fatal("mouse down did not start transcript selection")
	}

	current = updateModel(t, current, tea.MouseMotionMsg(tea.Mouse{
		X:      4,
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
		X:      4,
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

func TestModelMouseClickWithoutDragDoesNotCopy(t *testing.T) {
	t.Parallel()

	current := newModel(make(chan runRequest), make(chan struct{}))
	current = updateModel(t, current, tea.WindowSizeMsg{Width: 40, Height: 14})
	current.viewport.SetContent("alpha")
	viewportTop := lipgloss.Height(current.headerView(current.width))

	current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{
		X:      1,
		Y:      viewportTop,
		Button: tea.MouseLeft,
	}))
	updated, command := current.Update(tea.MouseReleaseMsg(tea.Mouse{
		X:      1,
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
			top := lipgloss.Height(current.headerView(current.width))
			copySelection := func() {
				current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{X: 0, Y: top, Button: tea.MouseLeft}))
				updated, _ := current.Update(tea.MouseReleaseMsg(tea.Mouse{X: 4, Y: top, Button: tea.MouseLeft}))
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
				!strings.Contains(current.activityIndicator(), "Thinking...") {
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
			top := len(lines) - lipgloss.Height(current.footerView(width)) -
				lipgloss.Height(current.composerView(width)) - 3
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
		top := lipgloss.Height(current.headerView(40))
		current = updateModel(t, current, tea.MouseClickMsg(tea.Mouse{X: 0, Y: top, Button: tea.MouseLeft}))
		updated, cmd := current.Update(tea.MouseReleaseMsg(tea.Mouse{X: 4, Y: top, Button: tea.MouseLeft}))
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
