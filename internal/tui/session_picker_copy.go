package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const sessionPickerCopyLabel = " ⧉ "

func (m model) sessionPickerCopyVisible() bool {
	p := m.sessionPicker
	if p.previewID == "" || !p.previewVisible || p.rename != nil || p.restoring || m.width < 16 || m.height < 8 {
		return false
	}
	header, _ := sessionPreviewParts(p.previewText)
	return header != "" && (m.sessionPickerLayout().wide || p.previewFocused)
}

func (m model) sessionPickerCopyHovered() bool {
	p := m.sessionPicker
	if p.pointer == nil || !m.sessionPickerCopyVisible() {
		return false
	}
	l := m.sessionPickerLayout()
	x := l.x + 2
	if l.wide {
		x += l.listWidth + 3
	}
	header, _ := sessionPreviewParts(p.previewText)
	heading := ansi.Truncate(sanitizeToolDetail(header, false), l.previewWidth-ansi.StringWidth(sessionPickerCopyLabel), "…")
	x += ansi.StringWidth(heading)
	return p.pointer.Y == l.y+3 && p.pointer.X >= x && p.pointer.X < x+ansi.StringWidth(sessionPickerCopyLabel)
}

func (m *model) trackSessionPickerCopy(message tea.Msg) (tea.Cmd, bool) {
	p := m.sessionPicker
	switch mouse := message.(type) {
	case tea.MouseClickMsg:
		p.copyPressedID = ""
		if m.sessionPickerCopyHovered() && mouse.Button == tea.MouseLeft {
			p.copyPressedID = p.previewID
			return nil, true
		}
	case tea.MouseReleaseMsg:
		pressed := p.copyPressedID
		p.copyPressedID = ""
		if pressed != "" && pressed == p.previewID && mouse.Button == tea.MouseLeft && m.sessionPickerCopyHovered() {
			p.copiedID = pressed
			return m.copyText(pressed), true
		}
	}
	return nil, false
}
