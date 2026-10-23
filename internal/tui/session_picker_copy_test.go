package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func copyPickerModel(t *testing.T, width int) model {
	t.Helper()
	m := pickerModel(t, width, 24)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	return updateModel(t, m, sessionPreviewResult{generation: m.sessionPreviewGeneration,
		text: "Last activity · 2026-09-18 12:34\n\nYou\nFirst preview"})
}

func TestSessionPickerCopyIDHoverAndClick(t *testing.T) {
	t.Parallel()
	for _, width := range []int{60, 160} {
		m := copyPickerModel(t, width)
		mouse := findPainted(t, m, "⧉")
		idle := m.View().Content
		m = updateModel(t, m, tea.MouseMotionMsg(mouse))
		if m.View().Content == idle || !strings.Contains(m.sessionPickerView(), "Copy session ID") {
			t.Fatal("copy hover has no visual feedback")
		}
		canvas := lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewLayer(m.View().Content))
		assertColor(t, canvas.CellAt(mouse.X, mouse.Y).Style.Fg, secondaryColor)
		assertColor(t, canvas.CellAt(mouse.X, mouse.Y).Style.Bg, inkBlackColor)
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		next, command := m.Update(tea.MouseReleaseMsg(mouse))
		m = next.(model)
		if command == nil || fmt.Sprint(command().(tea.BatchMsg)[0]()) != "one" {
			t.Fatal("copy did not send the session ID to the clipboard")
		}
		if !strings.Contains(m.sessionPickerView(), "Session ID copied") || m.input.Value() != "keep my draft" {
			t.Fatal("copy confirmation missing or draft changed")
		}
		m = updateModel(t, m, copyNoticeExpiredMsg(m.copyGeneration))
		if strings.Contains(m.sessionPickerView(), "Session ID copied") {
			t.Fatal("copy confirmation did not expire")
		}
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		next, command = m.Update(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})
		m = next.(model)
		if command != nil || m.copyNotice {
			t.Fatal("release outside copied an ID")
		}
		mouse.Button = tea.MouseRight
		m = updateModel(t, m, tea.MouseClickMsg(mouse))
		_, command = m.Update(tea.MouseReleaseMsg(mouse))
		if command != nil {
			t.Fatal("right click copied an ID")
		}
	}
}

func TestSessionPickerCopyTracksDisplayedPreview(t *testing.T) {
	t.Parallel()
	m := copyPickerModel(t, 160)
	mouse := findPainted(t, m, "⧉")
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.sessionPicker.previewID != "one" || selectedSessionKey(m.sessionPicker) != "two" {
		t.Fatal("pending selection changed the displayed preview ID")
	}
	_, command := m.Update(tea.MouseReleaseMsg(mouse))
	if command != nil {
		t.Fatal("selection change did not cancel a pending copy press")
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	_, command = m.Update(tea.MouseReleaseMsg(mouse))
	if command == nil || fmt.Sprint(command().(tea.BatchMsg)[0]()) != "one" {
		t.Fatal("copy used the new selection instead of the displayed preview")
	}
	m = updateModel(t, m, sessionPreviewResult{generation: m.sessionPreviewGeneration,
		text: "Last activity · 2026-09-18 13:00\n\nSecond preview"})
	if m.sessionPicker.previewID != "two" {
		t.Fatal("loaded preview retained the previous ID")
	}
	m = updateModel(t, m, tea.MouseClickMsg(mouse))
	m = updateModel(t, m, sessionPreviewResult{generation: m.sessionPreviewGeneration, err: errors.New("missing file")})
	_, command = m.Update(tea.MouseReleaseMsg(mouse))
	if command != nil || strings.Contains(m.sessionPickerView(), "⧉") {
		t.Fatal("error preview retained the copy button or a pending press")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.sessionPicker.previewID != "" || m.sessionPickerCopyVisible() {
		t.Fatal("group heading exposed a session copy action")
	}
}
