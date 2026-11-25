package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func (p *settingsPanel) editTarget(x, y int) string {
	row := y - 4
	if row < 0 || row >= p.layout.bodyHeight {
		return ""
	}
	if p.collection != nil {
		d := p.collection
		if d.editing {
			if row%2 == 1 && row/2 < len(d.cells) {
				return fmt.Sprintf("cell:%d", row/2)
			}
			return ""
		}
		start := max(0, d.row-p.layout.bodyHeight+5)
		if row+start < d.count() && row < p.layout.bodyHeight-4 {
			return fmt.Sprintf("collection:%d", row+start)
		}
		return ""
	}
	if a := p.action; a != nil {
		if menu, selection := a.activeMenu(); menu != nil {
			layout := p.actionMenuLayout(menu, *selection)
			if layout.more {
				if row == len(layout.header) {
					return "action-next"
				}
				return ""
			}
			index := row - len(layout.header) + layout.start
			if row >= len(layout.header) && index >= layout.start && index < layout.end {
				return fmt.Sprintf("action:%d", index)
			}
			return ""
		}
		if row == 1 {
			return "editor"
		}
		return ""
	}
	if field := p.editing; field != nil && field.Kind == interaction.SettingEnum && !field.AllowCustom && !p.confirmUnset {
		start, end := p.visibleChoices()
		if row+start < end {
			return fmt.Sprintf("choice:%d", row+start)
		}
	}
	if row == 1 {
		return "editor"
	}
	return ""
}

// settingEditClick applies a click inside an editor as the action its row
// stands for: choosing a row is selecting it and pressing Enter.
func (m model) settingEditClick(target string) (tea.Model, tea.Cmd) {
	p := m.settings
	accept := inputActionMatch{action: inputActionSettingsAccept, matched: true, enabled: true}
	var index int
	switch {
	case target == "action-next":
		return m.handleSettingsAction(inputActionMatch{action: inputActionSettingsPage, argument: 1, matched: true, enabled: true})
	case target == "editor":
		return m, p.input.Focus()
	case sscan(target, "choice:%d", &index):
		p.choice = index
		return m.handleSettingsAction(accept)
	case sscan(target, "collection:%d", &index):
		p.collection.row = index
		return m.handleSettingsAction(accept)
	case sscan(target, "cell:%d", &index):
		d := p.collection
		d.cells[d.cell] = p.input.Value()
		d.cell = index
		p.input.SetValue(d.cells[d.cell])
		return m, p.input.Focus()
	case sscan(target, "action:%d", &index):
		if _, selection := p.action.activeMenu(); selection != nil {
			*selection = index
			return m.handleSettingsAction(accept)
		}
	}
	return m, nil
}

// sscan reports whether target has the format, filling value.
func sscan(target, format string, value *int) bool {
	n, err := fmt.Sscanf(target, format, value)
	return err == nil && n == 1
}
