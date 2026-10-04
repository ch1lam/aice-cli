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
		style := mutedStyle
		if i == p.tab {
			style = slashCommandSelectedStyle
		}
		parts = append(parts, style.Render(tab.Label))
	}
	joined := strings.Join(parts, mutedStyle.Render(" │ "))
	if ansi.StringWidth(joined) <= p.layout.inner-2 {
		return "  " + joined
	}
	// Keep the selected category visible in narrow terminals.
	if p.tab < len(parts) {
		return "  ‹ " + parts[p.tab] + " ›"
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

func settingSummary(field interaction.SettingField, width, height int) string {
	text := strings.TrimSpace(sanitizeMultilineText(field.Description))
	if field.Action != nil && field.Action.Name == "login" {
		text = "Manage sign-in and credentials for this account."
	} else if text == "" && field.Kind == interaction.SettingAction {
		text = "Open " + sanitizeSingleLineText(field.Label) + "."
	}
	// Descriptions can include runtime details after the initial explanation.
	text, _, _ = strings.Cut(text, "\n")
	if first, _, ok := strings.Cut(text, ". "); ok {
		text = first + "."
	}
	if first, _, ok := strings.Cut(text, "。"); ok {
		text = first + "。"
	}
	lines := strings.Split(ansi.Wrap(text, width, ""), "\n")
	if len(lines) > height {
		lines = lines[:height]
		lines[height-1] = ansi.Truncate(lines[height-1]+"…", width, "…")
	}
	return mutedStyle.Italic(true).Width(width).Height(height).
		Align(lipgloss.Center, lipgloss.Bottom).Render(strings.Join(lines, "\n"))
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
			details := sanitizeMultilineText(field.Description)
			if field.ID == "mcp.status" || field.ID == "mcp.services" {
				details = mcpSettingsDetails(details, l.inner)
			} else if field.ID == "project.skills" {
				details = skillsSettingsDetails(details, l.inner)
			}
			lines := strings.Split(ansi.Hardwrap(details, l.inner, true), "\n")
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
			start, end := p.visibleChoices()
			for i := start; i < end; i++ {
				choice := field.Choices[i]
				prefix := "  "
				style := bodyStyle
				if i == p.choice {
					prefix = "› "
					style = slashCommandSelectedStyle
				}
				rows = append(rows, style.Render(ansi.Truncate(
					sanitizeSingleLineText(prefix+choice.Label+"  "+choice.Description), l.inner, "…",
				)))
			}
			body = strings.Join(rows, "\n")
		} else {
			body = sanitizeSingleLineText(field.Label) + "\n" + p.input.View() + "\n\n" + sanitizeMultilineText(field.Description)
		}
	} else {
		fields := p.fields()
		listWidth, listHeight := p.listSize()
		hovered := ""
		if p.pointer != nil {
			hovered = p.target(*p.pointer)
		}
		var rows []string
		for _, row := range p.visibleSettingRows(fields) {
			if row.field < 0 {
				rows = append(rows, p.sectionHeading(row, listWidth, hovered))
				continue
			}
			rows = append(rows, settingRow(fields[row.field], row.field == p.selection, listWidth))
		}
		if len(rows) == 0 {
			rows = []string{"No matching settings"}
		}
		body = lipgloss.NewStyle().Width(listWidth).Height(listHeight).Render(strings.Join(rows, "\n"))
		if summaryHeight := p.summaryHeight(); summaryHeight > 0 && len(fields) > 0 && !p.selectedSectionCollapsed(fields) {
			body += "\n\n" + settingSummary(fields[min(p.selection, len(fields)-1)], l.inner, summaryHeight)
		}
	}
	body = lipgloss.NewStyle().Width(l.inner).Height(l.bodyHeight).MaxHeight(l.bodyHeight).Render(body)
	search := mutedStyle.Render("  / Search all settings")
	if p.search {
		input := p.input
		input.Prompt = "  "
		input.Placeholder = "/ Search all settings"
		search = input.View()
	}
	notice := p.notice
	if p.loading {
		notice = "Loading…"
	}
	if notice != "" {
		search = mutedStyle.Render(ansi.Truncate("  "+sanitizeSingleLineText(notice), l.inner, "…"))
	}
	footer, footerX := settingsFooterLayout(m.settingsFooter(), l.inner)
	content := strings.Join([]string{
		"", ansi.Truncate(p.tabs(), l.inner, "…"), "", ansi.Truncate(search, l.inner, "…"),
		"", body, "",
		strings.Repeat(" ", footerX) + mutedStyle.Render(footer),
	}, "\n")
	hover := p.pointer != nil && modalCloseContains(l, *p.pointer)
	return modalFrame(title, content, l, hover, p.pressed == "close")
}

func (m model) settingsFooter() string {
	p := m.settings
	footer := p.footer()
	if m.canContinueTask() {
		footer = "[Continue] F6 · new run · Esc back"
	}
	if m.running && !p.usage {
		footer = "[Stop current run] F6 · Esc back"
		if m.cancelRequested {
			footer = "Stopping… · Esc back"
		}
	}
	return footer
}

// Use the same truncated text and offset for painting and footer hit testing.
func settingsFooterLayout(text string, width int) (string, int) {
	text = ansi.Truncate(text, width, "…")
	return text, max(0, (width-ansi.StringWidth(text))/2)
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
			cursor.Y += l.y + 4
			if p.editing != nil {
				cursor.Y = l.y + 7
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
	x, y := mouse.X-l.x-2, mouse.Y-l.y-2
	if mouse.X < l.x || mouse.X >= l.x+l.width || mouse.Y < l.y || mouse.Y >= l.y+l.height {
		return "outside"
	}
	if x < 0 || x >= l.inner || y < 0 || y > l.height-4 {
		return ""
	}
	if p.editing != nil {
		return p.editTarget(x, y)
	}
	if y == 0 {
		if x < 2 {
			return ""
		}
		x -= 2
		offset := 0
		if p.tabsWidth() > l.inner-2 {
			if x < (l.inner-2)/2 {
				return "previous"
			}
			return "next"
		}
		for i, tab := range p.snapshot.Categories {
			w := ansi.StringWidth(tab.Label)
			if x >= offset && x < offset+w {
				return fmt.Sprintf("tab:%d", i)
			}
			offset += w + 3
		}
	}
	if y == 2 {
		return "search"
	}
	listWidth, _ := p.listSize()
	row := y - 4
	fields := p.fields()
	rows := p.visibleSettingRows(fields)
	if row >= 0 && row < len(rows) && x < listWidth {
		if rows[row].field >= 0 {
			return "field:" + fields[rows[row].field].ID
		}
		if rows[row].key != "" {
			return "section:" + rows[row].key
		}
	}
	return ""
}

func (m model) settingsPointer(message tea.Msg) (tea.Model, tea.Cmd) {
	p := m.settings
	switch event := message.(type) {
	case tea.BlurMsg:
		p.pointer = nil
	case tea.MouseMotionMsg:
		mouse := event.Mouse()
		p.pointer = &mouse
	case tea.MouseClickMsg:
		mouse := event.Mouse()
		p.pointer = &mouse
		if event.Button == tea.MouseLeft {
			p.pressed = m.settingsTarget(mouse)
			p.pressedLayout = p.layout
		}
	case tea.MouseReleaseMsg:
		mouse := event.Mouse()
		p.pointer = &mouse
		target := m.settingsTarget(mouse)
		pressed := p.pressed
		p.pressed = ""
		if event.Button != tea.MouseLeft || pressed == "" || pressed != target || p.pressedLayout != p.layout {
			return m, nil
		}
		if target == "continue-task" {
			return m.continueTask()
		}
		if target == "stop-run" {
			m.requestRunCancellation()
			return m, nil
		}
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
			p.searchCollapsed = nil
			p.selection = 0
			p.input.SetValue("")
			return m, p.input.Focus()
		case strings.HasPrefix(target, "section:"):
			key := strings.TrimPrefix(target, "section:")
			p.setSectionCollapsed(key, !p.sectionCollapsed(key))
			// Folding changes the rows under the pointer. Require fresh motion
			// before showing hover again instead of retaining the click highlight.
			p.pointer = nil
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
		mouse := event.Mouse()
		p.pointer = &mouse
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
			p.moveSettingSelection(delta)
		}
	}
	return m, nil
}

func (m model) settingsTarget(mouse tea.Mouse) string {
	p := m.settings
	if m.running && !p.usage && mouse.Y == p.layout.y+p.layout.height-2 {
		_, footerX := settingsFooterLayout(m.settingsFooter(), p.layout.inner)
		x := mouse.X - p.layout.x - 2 - footerX
		if !m.cancelRequested && x >= 0 && x < min(p.layout.inner, len("[Stop current run]")) {
			return "stop-run"
		}
		// The run controls replace, rather than overlay, the editor footer.
		return ""
	}
	if m.canContinueTask() && mouse.Y == p.layout.y+p.layout.height-2 {
		_, footerX := settingsFooterLayout(m.settingsFooter(), p.layout.inner)
		x := mouse.X - p.layout.x - 2 - footerX
		if x >= 0 && x < min(p.layout.inner, len("[Continue]")) {
			return "continue-task"
		}
		return ""
	}
	return p.target(mouse)
}
