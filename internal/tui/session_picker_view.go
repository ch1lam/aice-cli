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
	w, h := max(8, m.width-4), max(8, m.height-2)
	l := sessionPickerLayout{width: w, height: h, inner: w - 4, bodyHeight: h - 7}
	l.x, l.y = max(0, (m.width-w)/2), max(0, (m.height-h)/2)
	l.wide = w >= 84 && m.sessionPicker != nil && m.sessionPicker.previewVisible
	l.listWidth, l.previewWidth = l.inner-2, l.inner-2
	if l.wide {
		l.listWidth = (l.inner - 5) / 2
		l.previewWidth = l.inner - l.listWidth - 5
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
	if p.rename != nil {
		p.rename.input.SetWidth(max(1, l.inner-2))
	}
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
	body := m.sessionPickerPane(listView, l.listWidth, !p.previewFocused && !p.input.Focused())
	if l.wide {
		preview := m.sessionPickerPane(p.preview.View(), l.previewWidth, p.previewFocused)
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, " ", preview)
	} else if p.previewFocused {
		body = m.sessionPickerPane(p.preview.View(), l.previewWidth, true)
	}
	count := len(p.list.Items())
	title := fmt.Sprintf("SESSIONS · CURRENT PROJECT  %d/%d", min(count, p.list.Index()+1), count)
	if p.previewFocused {
		title = "SESSIONS · PREVIEW"
	}
	help := "/ search · → preview · Esc close · ↑↓ select · Enter resume · F2 rename · F4 read"
	if p.previewVisible {
		help = "/ search · ←→ focus · Esc hide preview · ↑↓ select · Enter resume · F2 rename · F4 read"
	}
	if p.previewFocused {
		help = "/ search · ←→ focus · Esc hide preview · ↑↓ scroll · Enter resume"
	}
	if l.inner < 55 {
		help = "/ search · → preview · Esc close · ↑↓ · Enter"
		if p.previewVisible {
			help = "/ search · ←→ focus · Esc hide preview · ↑↓ · Enter"
		}
	}
	if p.loading {
		if p.input.Value() == "" {
			title += " · loading older sessions"
		} else {
			title += " · searching text"
		}
	}
	input := p.input.View()
	if p.rename != nil {
		title = "RENAME SESSION · blank restores automatic title"
		input = p.rename.input.View()
		help = "Enter save · Esc cancel"
		if p.rename.saving {
			help = "Saving title… · Esc closes"
		}
	}
	if p.notice != "" {
		help = sanitizeToolDetail(p.notice, false)
	}
	content := strings.Join([]string{
		"", input, body,
		mutedStyle.Render(ansi.Truncate(help, l.inner, "…")),
	}, "\n")
	frame := slashCommandMenuStyle.Foreground(primaryTextColor).Background(inkBlackColor).
		BorderBackground(inkBlackColor).Width(l.width).Render(content)
	// Put controls on the border without shifting content or its mouse/IME rows.
	_, rest, _ := strings.Cut(frame, "\n")
	return m.sessionPickerTopBorder(title, l) + "\n" + rest
}

const sessionPickerCloseLabel = " ✕ "

func (m model) sessionPickerTopBorder(title string, l sessionPickerLayout) string {
	style := mutedStyle.Background(inkBlackColor)
	if m.sessionPickerCloseHovered() {
		style = labelStyle.Foreground(inkBlackColor).Background(secondaryColor)
		if m.sessionPicker.closePressed {
			style = style.Background(accentColor)
		}
	}
	titleWidth := max(0, l.inner-ansi.StringWidth(sessionPickerCloseLabel)-1)
	heading := ansi.Truncate(" "+title+" ", titleWidth, "…")
	gap := l.inner - ansi.StringWidth(heading) - ansi.StringWidth(sessionPickerCloseLabel)
	border := slashCommandMenuStyle.GetBorderStyle()
	stroke := lipgloss.NewStyle().Foreground(slashCommandMenuStyle.GetBorderTopForeground()).Background(inkBlackColor)
	return stroke.Render(border.TopLeft+border.Top) + bodyStyle.Bold(true).Background(inkBlackColor).Render(heading) +
		stroke.Render(strings.Repeat(border.Top, gap)) + style.Render(sessionPickerCloseLabel) +
		stroke.Render(border.Top+border.TopRight)
}

func (m model) sessionPickerCloseHovered() bool {
	p := m.sessionPicker
	if p.closePointer == nil || m.width < 16 || m.height < 8 {
		return false
	}
	l := m.sessionPickerLayout()
	x, y := p.closePointer.X-l.x-2, p.closePointer.Y-l.y
	return y == 0 && x >= l.inner-ansi.StringWidth(sessionPickerCloseLabel) && x < l.inner
}

// Activate only after a left press and release within the painted button.
func (m *model) trackSessionPickerClose(message tea.Msg) bool {
	p := m.sessionPicker
	switch mouse := message.(type) {
	case tea.MouseMotionMsg:
		pointer := tea.Mouse(mouse)
		p.closePointer = &pointer
	case tea.MouseClickMsg:
		pointer := tea.Mouse(mouse)
		p.closePointer = &pointer
		p.closePressed = mouse.Button == tea.MouseLeft && m.sessionPickerCloseHovered()
	case tea.MouseReleaseMsg:
		pointer := tea.Mouse(mouse)
		p.closePointer = &pointer
		close := p.closePressed && mouse.Button == tea.MouseLeft && m.sessionPickerCloseHovered()
		p.closePressed = false
		return close
	}
	return false
}

func (m model) sessionPickerPane(content string, width int, focused bool) string {
	style := slashCommandMenuStyle.Padding(0).
		Foreground(primaryTextColor).Background(inkBlackColor).
		BorderBackground(inkBlackColor).Width(width + 2)
	if focused && m.sessionPicker.rename == nil && !m.sessionPicker.restoring {
		style = style.BorderForeground(secondaryColor)
	}
	return style.Render(content)
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
	if (m.sessionPicker.input.Focused() || m.sessionPicker.rename != nil) && !m.sessionPicker.restoring {
		cursor = m.sessionPicker.input.Cursor()
		if m.sessionPicker.rename != nil {
			cursor = m.sessionPicker.rename.input.Cursor()
			if m.sessionPicker.rename.saving {
				cursor = nil
			}
		}
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
	if y < 2 || y > 3+l.bodyHeight || (l.wide && x == l.listWidth+2) {
		return m, nil
	}
	if (l.wide && x >= l.listWidth+3) || (!l.wide && p.previewFocused) {
		p.previewFocused = true
		p.input.Blur()
		return m, nil
	}
	p.previewFocused = false
	p.input.Blur()
	if y == 2 || y == 3+l.bodyHeight || x == 0 || x == l.listWidth+1 {
		return m, nil
	}
	row := (y - 3) / (sessionItemDelegate{}.Height() + sessionItemDelegate{}.Spacing())
	if row >= p.list.Paginator.PerPage {
		return m, nil
	}
	index := p.list.Paginator.Page*p.list.Paginator.PerPage + row
	if index < len(p.list.Items()) {
		p.list.Select(index)
		return m, m.requestSessionPreview()
	}
	return m, nil
}
