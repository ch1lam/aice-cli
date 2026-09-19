package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestPickerPressCannotSurviveInterruptedGesture(t *testing.T) {
	for _, interruption := range []string{"blur", "resize", "key", "wheel", "drag back", "wrong button"} {
		t.Run(interruption, func(t *testing.T) {
			m := pickerModel(t, 100, 28)
			mouse := sessionCloseMouse(t, m)
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			if !m.sessionPicker.closePressed {
				t.Fatal("fixture did not press close")
			}
			switch interruption {
			case "blur":
				m = updateModel(t, m, tea.BlurMsg{})
			case "resize":
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 28})
			case "key":
				m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyF1})
			case "wheel":
				m = updateModel(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelLeft})
			case "drag back":
				away := mouse
				away.Y++
				m = updateModel(t, m, tea.MouseMotionMsg(away))
				m = updateModel(t, m, tea.MouseMotionMsg(mouse))
			case "wrong button":
				other := mouse
				other.Button = tea.MouseRight
				m = updateModel(t, m, tea.MouseReleaseMsg(other))
			}
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if m.sessionPicker == nil {
				t.Fatal("interrupted close press activated on a later release")
			}
			if m.capture.active || m.sessionPicker.closePressed {
				t.Fatal("release retained pointer capture")
			}
		})
	}
}

func TestPointerGeometryMatchesPaintedComposer(t *testing.T) {
	for _, size := range [][2]int{{24, 10}, {60, 24}, {120, 40}} {
		m := newModel(nil, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updateModel(t, m, tea.PasteMsg{Content: "中文 marker\nsecond"})
		mouse := paintedMouse(t, m, "中文 marker")
		layout := m.screenLayout()
		if !layout.composer.contains(mouse) || layout.transcript.contains(mouse) {
			t.Fatal("paint and composer hit bounds disagree")
		}
		if m.View().Cursor == nil || !layout.composer.contains(tea.Mouse{X: m.View().Cursor.X, Y: m.View().Cursor.Y}) {
			t.Fatal("real terminal caret is outside shared composer bounds")
		}
		outside := tea.Mouse{X: layout.composer.x + layout.composer.width, Y: layout.composer.y}
		if layout.composer.contains(outside) {
			t.Fatal("rectangle right edge is not half-open")
		}
	}
}

func TestLocalCommandMenuDoesNotBlockVisibleTranscriptClick(t *testing.T) {
	m := foldTestModel()
	m.commandMenu = &commandMenuState{}
	m.resizeLayout()
	m = clickPainted(t, m, "✓ read")
	if !m.foldExpanded(foldTarget{kind: foldTool, id: 2}) {
		t.Fatal("local command chooser blocked visible transcript fold")
	}
	if m.commandMenu == nil {
		t.Fatal("transcript action dismissed the command chooser")
	}
}

func TestPointerCaptureCancelsOnComposerReflow(t *testing.T) {
	m := foldTestModel()
	mouse := paintedMouse(t, m, "✓ read")
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	if !m.selection.active {
		t.Fatal("fixture did not start a selection")
	}
	m.inputNotice = "a notice changes the composer height"
	m.resizeLayout()
	m = updateModel(t, m, struct{}{})
	m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
	if m.foldExpanded(foldTarget{kind: foldTool, id: 2}) || m.selection.active {
		t.Fatal("reflow retained the old click target")
	}
}

func TestAsyncActionsReflowExpandedHelp(t *testing.T) {
	for _, width := range []int{24, 40, 80, 120} {
		m := newModel(nil, nil)
		m.help.ShowAll, m.running = true, true
		m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
		m = updateModel(t, m, runBatchMsg{updates: []runUpdate{{active: &activeRunFunc{}}}})
		if actual := lipgloss.Height(m.footerView(m.layoutWidth())); actual != m.chrome.footer {
			t.Fatalf("width %d: measured footer %d, painted %d", width, m.chrome.footer, actual)
		}
	}
}
