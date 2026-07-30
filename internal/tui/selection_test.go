package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	if got, want := fmt.Sprint(command()), "alpha"; got != want {
		t.Errorf("clipboard content = %q, want %q", got, want)
	}
	if current.selection.active {
		t.Fatal("selection remains active after mouse release")
	}
	if current.status != "Selected text copied" {
		t.Errorf("status = %q, want copy confirmation", current.status)
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
