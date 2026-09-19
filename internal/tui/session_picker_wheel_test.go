package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSessionPickerWheelFollowsPointerWithoutTakingFocus(t *testing.T) {
	t.Parallel()
	for _, pane := range []sessionPickerPane{sessionPaneList, sessionPanePreview} {
		for _, searchFocused := range []bool{false, true} {
			t.Run(fmt.Sprintf("pane=%d/search=%v", pane, searchFocused), func(t *testing.T) {
				m := pickerModel(t, 160, 24)
				p := m.sessionPicker
				p.previewVisible = true
				p.previewFocused = pane == sessionPaneList && !searchFocused
				if searchFocused {
					p.input.Focus()
				}
				p.previewText = strings.Repeat("Preview line\n\n", 100)
				m.resizeSessionPicker()
				requested := 0
				m.previewSession = func(generation uint64, key, query string) (tea.Cmd, context.CancelFunc) {
					requested++
					return func() tea.Msg { return sessionPreviewResult{generation: generation} }, func() {}
				}
				l := m.sessionPickerLayout()
				x := l.x + 2
				if pane == sessionPanePreview {
					x += l.listWidth + 3
				}
				previewFocused := p.previewFocused
				m = updateModel(t, m, tea.MouseWheelMsg{X: x, Y: l.y + 3, Button: tea.MouseWheelDown})
				if p.previewFocused != previewFocused || p.input.Focused() != searchFocused {
					t.Fatal("wheel changed keyboard focus")
				}
				if pane == sessionPaneList {
					if selectedSessionKey(p) != "two" || requested != 1 || p.preview.YOffset() != 0 {
						t.Fatal("list wheel did not select and request exactly one preview")
					}
				} else if selectedSessionKey(p) != "one" || requested != 0 || p.preview.YOffset() != 3 {
					t.Fatal("preview wheel did not scroll only the preview")
				}
			})
		}
	}
}

func TestSessionPickerWheelIgnoresUnsupportedAndOutsideEvents(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 160, 24)
	p := m.sessionPicker
	p.previewVisible = true
	p.previewText = strings.Repeat("Preview line\n\n", 100)
	m.resizeSessionPicker()
	l := m.sessionPickerLayout()
	x, y := l.x+2, l.y+3
	for _, mouse := range []tea.MouseWheelMsg{
		{X: x, Y: y, Button: tea.MouseWheelLeft},
		{X: x, Y: y, Button: tea.MouseWheelRight},
		{X: x - 1, Y: y, Button: tea.MouseWheelDown},
		{X: x + l.inner, Y: y, Button: tea.MouseWheelDown},
		{X: x, Y: y - 1, Button: tea.MouseWheelDown},
		{X: x, Y: y + l.bodyHeight, Button: tea.MouseWheelDown},
		{X: x + l.listWidth, Y: y, Button: tea.MouseWheelDown},
		{X: x + l.listWidth + 1, Y: y, Button: tea.MouseWheelDown},
		{X: x + l.listWidth + 2, Y: y, Button: tea.MouseWheelDown},
	} {
		generation := m.sessionPreviewGeneration
		next, command := m.Update(mouse)
		m = next.(model)
		if command != nil || selectedSessionKey(p) != "one" || p.preview.YOffset() != 0 || m.sessionPreviewGeneration != generation {
			t.Fatalf("unsupported or outside wheel changed picker: %+v", mouse)
		}
	}
}

func TestSessionPickerWheelAtListBoundaryKeepsPendingPreview(t *testing.T) {
	t.Parallel()
	m := pickerModel(t, 100, 28)
	p := m.sessionPicker
	p.previewVisible = true
	p.list.Select(len(p.list.Items()) - 1)
	cancelled := false
	p.cancelPreview = func() { cancelled = true }
	m.resizeSessionPicker()
	l := m.sessionPickerLayout()
	generation := m.sessionPreviewGeneration
	next, command := m.Update(tea.MouseWheelMsg{X: l.x + 2, Y: l.y + 3, Button: tea.MouseWheelDown})
	m = next.(model)
	if command != nil || cancelled || m.sessionPreviewGeneration != generation {
		t.Fatal("wheel at boundary restarted preview work")
	}
}

func TestSessionPickerWheelBetweenGroupsUpdatesPreview(t *testing.T) {
	t.Parallel()
	m := groupedPickerModel(t)
	p := m.sessionPicker
	p.previewVisible = true
	p.collapsedGroups = map[string]bool{"Today": true, "Yesterday": true, "Earlier": true}
	m.setSessionItems(p.results)
	p.list.Select(0)
	m.resizeSessionPicker()
	l := m.sessionPickerLayout()
	generation := m.sessionPreviewGeneration
	m = updateModel(t, m, tea.MouseWheelMsg{X: l.x + 2, Y: l.y + 3, Button: tea.MouseWheelDown})
	if m.sessionPreviewGeneration != generation+1 || !strings.Contains(p.previewText, "Yesterday") {
		t.Fatal("group-to-group movement did not update preview identity")
	}
}

func TestSessionPickerPaneBoundsAndNarrowWheel(t *testing.T) {
	t.Parallel()
	for _, width := range []int{60, 160} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/preview=%v", width, preview), func(t *testing.T) {
				m := pickerModel(t, width, 24)
				p := m.sessionPicker
				p.previewVisible, p.previewFocused = preview, preview
				p.previewText = strings.Repeat("Preview line\n\n", 100)
				m.resizeSessionPicker()
				l := m.sessionPickerLayout()
				x, y := l.x+2, l.y+3
				want := sessionPaneList
				if preview && !l.wide {
					want = sessionPanePreview
				}
				if m.sessionPickerPaneAt(x, y) != want || m.sessionPickerPaneAt(x+l.listWidth-1, y+l.bodyHeight-1) != want {
					t.Fatal("painted body boundaries were not included")
				}
				if l.wide && (m.sessionPickerPaneAt(x+l.listWidth+3, y) != sessionPanePreview ||
					m.sessionPickerPaneAt(x+l.inner-1, y+l.bodyHeight-1) != sessionPanePreview) {
					t.Fatal("painted preview boundaries were not included")
				}
				if !l.wide {
					m = updateModel(t, m, tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
					if preview && (p.preview.YOffset() != 3 || selectedSessionKey(p) != "one") {
						t.Fatal("narrow preview wheel reached hidden list")
					}
					if !preview && selectedSessionKey(p) != "two" {
						t.Fatal("narrow list wheel did not select a session")
					}
				}
			})
		}
	}
	m := pickerModel(t, 12, 7)
	if m.sessionPickerPaneAt(4, 4) != sessionPaneNone {
		t.Fatal("resize placeholder exposed an invisible pane")
	}
}
