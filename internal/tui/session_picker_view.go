package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type sessionPickerLayout struct {
	x, y, width, height, inner, bodyHeight, listWidth, previewWidth int
	wide                                                            bool
}

func (m model) sessionPickerLayout() sessionPickerLayout {
	w, h := max(8, min(112, m.width-4)), max(8, min(28, m.height-2))
	l := sessionPickerLayout{width: w, height: h, inner: w - 4, bodyHeight: h - 7}
	l.x, l.y = max(0, (m.width-w)/2), max(0, (m.height-h)/2)
	l.wide = w >= 84
	l.listWidth, l.previewWidth = l.inner, l.inner
	if l.wide {
		l.listWidth = (l.inner - 3) / 2
		l.previewWidth = l.inner - l.listWidth - 3
	}
	return l
}

func (m *model) resizeSessionPicker() {
	p := m.sessionPicker
	if p == nil {
		return
	}
	l := m.sessionPickerLayout()
	p.input.SetWidth(max(1, l.inner-2))
	p.list.SetSize(l.listWidth, l.bodyHeight)
	p.preview.SetWidth(l.previewWidth)
	p.preview.SetHeight(l.bodyHeight)
	p.preview.SetContent(ansi.Hardwrap(sanitizeToolDetail(p.previewText, true), l.previewWidth, true))
}

func (m model) sessionPickerView() string {
	p := m.sessionPicker
	l := m.sessionPickerLayout()
	listView := p.list.View()
	if len(p.list.Items()) == 0 {
		listView = "No matching sessions"
		if p.input.Value() == "" {
			listView = "No sessions yet in this project"
		}
		if p.loading {
			listView = "Searching sessions…"
		}
		listView = mutedStyle.Render(ansi.Hardwrap(listView, l.listWidth, true))
	}
	listView = lipgloss.NewStyle().Width(l.listWidth).Height(l.bodyHeight).MaxHeight(l.bodyHeight).Render(listView)
	body := listView
	if l.wide {
		divider := mutedStyle.Render(strings.TrimSuffix(strings.Repeat(" │ \n", l.bodyHeight), "\n"))
		body = lipgloss.JoinHorizontal(lipgloss.Top, listView, divider, p.preview.View())
	} else if p.previewFocused {
		body = p.preview.View()
	}
	count := len(p.list.Items())
	title := fmt.Sprintf("SESSIONS · CURRENT PROJECT  %d/%d", min(count, p.list.Index()+1), count)
	if p.previewFocused {
		title = "SESSIONS · PREVIEW"
	}
	help := "↑↓ select · Enter resume · F4 read · Tab preview · Esc close"
	if p.previewFocused {
		help = "↑↓ scroll · Enter resume · Tab search · Esc close"
	}
	if l.inner < 55 {
		help = "↑↓ · Enter resume · Tab · Esc"
	}
	if p.loading {
		if p.input.Value() == "" {
			title += " · loading older sessions"
		} else {
			title += " · searching text"
		}
	}
	if p.notice != "" {
		help = sanitizeToolDetail(p.notice, false)
	}
	content := strings.Join([]string{
		labelStyle.Render(ansi.Truncate(title, l.inner, "…")), p.input.View(), "", body, "",
		mutedStyle.Render(ansi.Truncate(help, l.inner, "…")),
	}, "\n")
	return lipgloss.NewStyle().Foreground(primaryTextColor).Background(inkBlackColor).
		Border(lipgloss.RoundedBorder()).BorderForeground(secondaryColor).BorderBackground(inkBlackColor).
		Padding(0, 1).Width(l.width).Render(content)
}

func (m model) overlaySessionPicker(content string) (string, *tea.Cursor) {
	if m.sessionPicker == nil {
		return content, nil
	}
	l := m.sessionPickerLayout()
	if m.width < 16 || m.height < 8 {
		return lipgloss.NewStyle().Width(max(1, m.width)).MaxHeight(max(1, m.height)).Render("Resize terminal\nEsc closes"), nil
	}
	canvas := lipgloss.NewCanvas(m.width, m.height).Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(restoreCanvasColors(m.sessionPickerView())).X(l.x).Y(l.y).Z(1),
	))
	var cursor *tea.Cursor
	if !m.sessionPicker.previewFocused && !m.sessionPicker.restoring {
		cursor = m.sessionPicker.input.Cursor()
		if cursor != nil {
			cursor.X += l.x + 2
			cursor.Y += l.y + 2
		}
	}
	return canvas.Render(), cursor
}

func (m model) clickSessionPicker(mouse tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	l := m.sessionPickerLayout()
	x, y := mouse.X-l.x-2, mouse.Y-l.y-1
	if mouse.Button != tea.MouseLeft || x < 0 || x >= l.inner {
		return m, nil
	}
	if y == 1 {
		p.previewFocused = false
		p.input.Focus()
		return m, nil
	}
	if y < 3 || y >= 3+l.bodyHeight {
		return m, nil
	}
	if (l.wide && x > l.listWidth) || (!l.wide && p.previewFocused) {
		p.previewFocused = true
		p.input.Blur()
		return m, nil
	}
	p.previewFocused = false
	p.input.Focus()
	index := p.list.Paginator.Page*p.list.Paginator.PerPage + (y-3)/4
	if index < len(p.list.Items()) {
		p.list.Select(index)
		return m, m.requestSessionPreview()
	}
	return m, nil
}
