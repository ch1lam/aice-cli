package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func (p *settingsPanel) tabs() string {
	var parts []string
	for i, tab := range p.snapshot.Categories {
		label := tab.Label
		if i == p.tab {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	joined := strings.Join(parts, " │ ")
	if ansi.StringWidth(joined) <= p.layout.inner {
		return joined
	}
	// Keep the selected category visible in narrow terminals.
	if p.tab < len(parts) {
		return "‹ " + parts[p.tab] + " ›"
	}
	return ""
}

func settingDetails(field interaction.SettingField, path string) string {
	parts := []string{field.Description}
	if field.Kind != interaction.SettingInfo && field.Kind != interaction.SettingAction {
		parts = append(parts, "Source: "+field.Source.Kind+" "+field.Source.Location)
		if field.Effective != "" {
			parts = append(parts, "Effective: "+field.Effective)
		}
		if field.Saved != nil {
			parts = append(parts, "Known saved: "+settingValueText(*field.Saved, field.InvertBool))
		} else {
			parts = append(parts, "Known saved: no user override")
		}
		if field.Inherited != nil {
			parts = append(parts, "Inherited: "+settingValueText(*field.Inherited, field.InvertBool))
		}
		parts = append(parts, "Save to: "+path, "Applies: "+string(field.Applies))
	}
	if field.DisabledReason != "" {
		parts = append(parts, field.DisabledReason)
	}
	return strings.Join(parts, "\n")
}

func (m model) settingsPanelView() string {
	p := m.settings
	l := p.layout
	title := "Settings"
	if p.usage {
		title = "Usage / Info"
	}
	var body string
	if field := p.editing; field != nil {
		title += " · " + field.Label
		if field.Kind == interaction.SettingInfo {
			lines := strings.Split(ansi.Hardwrap(sanitizeMultilineText(field.Description), l.inner, true), "\n")
			start := min(p.detailOffset, max(0, len(lines)-l.bodyHeight))
			body = strings.Join(lines[start:min(len(lines), start+l.bodyHeight)], "\n")
		} else if p.action != nil {
			body = p.actionView()
		} else if p.collection != nil {
			body = p.collectionView()
		} else if p.confirmUnset {
			body = "Remove the user override\n\n" + settingDetails(*field, p.snapshot.SavePath)
		} else if field.Kind == interaction.SettingEnum && !field.AllowCustom {
			var rows []string
			start := max(0, p.choice-(l.bodyHeight-2)+1)
			for i := start; i < min(len(field.Choices), start+l.bodyHeight-2); i++ {
				choice := field.Choices[i]
				prefix := "  "
				if i == p.choice {
					prefix = "› "
				}
				rows = append(rows, sanitizeSingleLineText(prefix+choice.Label+"  "+choice.Description))
			}
			body = strings.Join(rows, "\n")
		} else {
			body = sanitizeSingleLineText(field.Label) + "\n" + p.input.View() + "\n\n" + sanitizeMultilineText(field.Description)
		}
	} else {
		fields := p.fields()
		available := max(1, l.bodyHeight-5)
		wide := l.inner >= 88
		if wide {
			available = l.bodyHeight
		}
		start := max(0, p.selection-available+1)
		listWidth := l.inner
		if wide {
			listWidth = l.inner/2 - 2
		}
		var rows []string
		for i := start; i < min(len(fields), start+available); i++ {
			field := fields[i]
			prefix := "  "
			style := bodyStyle
			if i == p.selection {
				prefix = "› "
				style = labelStyle
			}
			value := settingValueText(field.Value, field.InvertBool)
			if field.Kind == interaction.SettingAction {
				value = "›"
			}
			if field.Kind == interaction.SettingInfo {
				value = field.Value.Text
			}
			label := field.Label
			if p.search && p.input.Value() != "" {
				label = field.Category + " · " + label
			}
			rows = append(rows, style.Render(ansi.Truncate(sanitizeSingleLineText(prefix+label+"  "+value), listWidth, "…")))
		}
		if len(rows) == 0 {
			rows = []string{"No matching settings"}
		}
		body = strings.Join(rows, "\n")
		if len(fields) > 0 {
			details := settingDetails(fields[min(p.selection, len(fields)-1)], p.snapshot.SavePath)
			if wide {
				body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(listWidth).Height(l.bodyHeight).Render(body), " │ ", mutedStyle.Width(l.inner-listWidth-3).Render(ansi.Hardwrap(sanitizeMultilineText(details), l.inner-listWidth-3, true)))
			} else {
				body += "\n\n" + mutedStyle.Render(ansi.Hardwrap(sanitizeMultilineText(details), l.inner, true))
			}
		}
	}
	body = lipgloss.NewStyle().Width(l.inner).Height(l.bodyHeight).MaxHeight(l.bodyHeight).Render(body)
	search := "/ Search all settings"
	if p.search {
		search = p.input.View()
	}
	notice := p.notice
	if p.loading {
		notice = "Loading…"
	}
	if notice != "" {
		search = sanitizeSingleLineText(notice)
	}
	content := strings.Join([]string{mutedStyle.Render(ansi.Truncate(p.tabs(), l.inner, "…")), ansi.Truncate(search, l.inner, "…"), body, mutedStyle.Render(ansi.Truncate(p.footer(), l.inner, "…"))}, "\n")
	hover := p.pointer != nil && modalCloseContains(l, *p.pointer)
	return modalFrame(title, content, l, hover, p.pressed == "close")
}

func (m model) overlaySettings(content string) (string, *tea.Cursor) {
	if !m.settingsVisible() {
		return content, nil
	}
	p := m.settings
	l := p.layout
	if m.width < 24 || m.height < 12 {
		return "Resize terminal · Esc closes Settings", nil
	}
	var cursor *tea.Cursor
	if p.input.Focused() && !p.saving {
		cursor = sessionPickerTextCursor(p.input)
		if cursor != nil {
			cursor.X += l.x + 2
			cursor.Y += l.y + 2
			if p.editing != nil {
				cursor.Y = l.y + 4
				if p.collection != nil && p.collection.editing {
					cursor.Y += 2 * p.collection.cell
				}
			}
		}
	}
	return composeModal(content, m.settingsPanelView(), m.width, m.height, l), cursor
}

func (p *settingsPanel) target(mouse tea.Mouse) string {
	l := p.layout
	if modalCloseContains(l, mouse) {
		return "close"
	}
	x, y := mouse.X-l.x-2, mouse.Y-l.y-1
	if mouse.X < l.x || mouse.X >= l.x+l.width || mouse.Y < l.y || mouse.Y >= l.y+l.height {
		return "outside"
	}
	if p.editing != nil {
		return p.editTarget(x, y)
	}
	if y == 0 {
		offset := 0
		if ansi.StringWidth(strings.Join(func() []string {
			var labels []string
			for i, t := range p.snapshot.Categories {
				label := t.Label
				if i == p.tab {
					label = "[" + label + "]"
				}
				labels = append(labels, label)
			}
			return labels
		}(), " │ ")) > l.inner {
			if x < l.inner/2 {
				return "previous"
			}
			return "next"
		}
		for i, tab := range p.snapshot.Categories {
			w := ansi.StringWidth(tab.Label)
			if i == p.tab {
				w += 2
			}
			if x >= offset && x < offset+w {
				return fmt.Sprintf("tab:%d", i)
			}
			offset += w + 3
		}
	}
	if p.editing != nil {
		return ""
	}
	if y == 1 {
		return "search"
	}
	available := max(1, l.bodyHeight-5)
	listWidth := l.inner
	if l.inner >= 88 {
		available = l.bodyHeight
		listWidth = l.inner/2 - 2
	}
	row := y - 2
	start := max(0, p.selection-available+1)
	if row >= 0 && row < available && x >= 0 && x < listWidth && row+start < len(p.fields()) {
		return "field:" + p.fields()[row+start].ID
	}
	return ""
}

func (m model) settingsPointer(message tea.Msg) (tea.Model, tea.Cmd) {
	p := m.settings
	switch event := message.(type) {
	case tea.MouseMotionMsg:
		mouse := event.Mouse()
		p.pointer = &mouse
	case tea.MouseClickMsg:
		mouse := event.Mouse()
		p.pointer = &mouse
		if event.Button == tea.MouseLeft {
			p.pressed = p.target(mouse)
			p.pressedLayout = p.layout
		}
	case tea.MouseReleaseMsg:
		mouse := event.Mouse()
		p.pointer = &mouse
		target := p.target(mouse)
		if event.Button != tea.MouseLeft || p.pressed == "" || p.pressed != target || p.pressedLayout != p.layout {
			return m, nil
		}
		p.pressed = ""
		if p.editing != nil && target != "close" && target != "outside" {
			return m.settingEditClick(target)
		}
		switch {
		case target == "close" || target == "outside":
			if p.editing != nil && !p.saving {
				p.notice = "Unsaved field draft · Esc discards this field"
			} else {
				m.closeSettings()
			}
			return m, nil
		case target == "next":
			return m.handleSettings(tea.KeyPressMsg{Code: tea.KeyTab})
		case target == "previous":
			return m.handleSettings(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		case strings.HasPrefix(target, "tab:"):
			if p.editing != nil {
				return m, nil
			}
			var tab int
			_, _ = fmt.Sscanf(target, "tab:%d", &tab)
			p.positions[p.tab] = p.selection
			p.tab = tab
			p.selection = p.positions[tab]
		case target == "search":
			p.notice = ""
			p.positions[p.tab] = p.selection
			p.search = true
			p.input.SetValue("")
			return m, p.input.Focus()
		case strings.HasPrefix(target, "field:"):
			id := strings.TrimPrefix(target, "field:")
			for i, field := range p.fields() {
				if field.ID == id {
					p.selection = i
					return m.beginSettingEdit(false)
				}
			}
		}
	case tea.MouseWheelMsg:
		delta := 0
		if event.Button == tea.MouseWheelDown {
			delta = 1
		}
		if event.Button == tea.MouseWheelUp {
			delta = -1
		}
		if p.action != nil {
			deltaKey := tea.KeyDown
			if delta < 0 {
				deltaKey = tea.KeyUp
			}
			return m.settingActionKey(tea.KeyPressMsg{Code: deltaKey})
		} else if p.collection != nil {
			if !p.collection.editing {
				p.collection.row = max(0, min(p.collection.row+delta, p.collection.count()-1))
			}
		} else if p.editing != nil && p.editing.Kind == interaction.SettingInfo {
			p.detailOffset = max(0, p.detailOffset+delta)
		} else if p.editing != nil {
			p.choice = max(0, min(p.choice+delta, len(p.editing.Choices)-1))
		} else {
			p.selection = max(0, min(p.selection+delta, len(p.fields())-1))
		}
	}
	return m, nil
}
