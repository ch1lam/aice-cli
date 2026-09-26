package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

func (p *settingsPanel) editTarget(x, y int) string {
	if y == p.layout.height-3 {
		if x >= 0 && x < 6 {
			return "save"
		}
		if x >= 7 && x < 15 {
			return "cancel"
		}
	}
	row := y - 2
	if row < 0 {
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
		if row == max(1, p.layout.bodyHeight-4) {
			switch {
			case x < 5:
				return "add"
			case x >= 6 && x < 14:
				return "delete"
			case x >= 15 && x < 18:
				return "move-up"
			case x >= 19 && x < 22:
				return "move-down"
			}
		}
		start := max(0, d.row-p.layout.bodyHeight+5)
		if row+start < d.count() && row < p.layout.bodyHeight-4 {
			return fmt.Sprintf("collection:%d", row+start)
		}
		return ""
	}
	if a := p.action; a != nil {
		var menu *interaction.CommandMenu
		if a.prompt != nil {
			menu = a.prompt.Menu
		} else if len(a.menus) > 0 {
			menu = a.menus[len(a.menus)-1]
		}
		if menu != nil {
			layout := p.actionMenuLayout(menu)
			if layout.more {
				if row == len(layout.header) {
					return "action-next"
				}
				return ""
			}
			index := row - len(layout.header) + layout.start
			if row >= len(layout.header) && index >= layout.start && index < layout.end {
				return "action:" + menu.Options[index].Label + ":" + menu.Options[index].Arguments
			}
			return ""
		}
		if row == 1 {
			return "editor"
		}
		return ""
	}
	if field := p.editing; field != nil && field.Kind == interaction.SettingEnum && !field.AllowCustom && !p.confirmUnset {
		start := max(0, p.choice-(p.layout.bodyHeight-2)+1)
		if row+start < len(field.Choices) {
			return "choice:" + field.Choices[row+start].Value
		}
	}
	if row == 1 {
		return "editor"
	}
	return ""
}
func (m model) settingEditClick(target string) (tea.Model, tea.Cmd) {
	p := m.settings
	switch {
	case target == "action-next":
		return m.settingActionKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	case target == "add":
		return m.collectionKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	case target == "delete":
		return m.collectionKey(tea.KeyPressMsg{Code: tea.KeyDelete})
	case target == "move-up":
		return m.collectionKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	case target == "move-down":
		return m.collectionKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl})
	case target == "cancel":
		return m.handleSettings(tea.KeyPressMsg{Code: tea.KeyEscape})
	case target == "save":
		if p.collection != nil {
			if p.collection.editing {
				next, cmd := m.collectionKey(tea.KeyPressMsg{Code: tea.KeyEnter})
				m = next.(model)
				if m.settings.collection.editing {
					return m, cmd
				}
			}
			return m.collectionKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		}
		return m.handleSettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	case target == "editor":
		return m, p.input.Focus()
	case strings.HasPrefix(target, "choice:"):
		for i, c := range p.editing.Choices {
			if target == "choice:"+c.Value {
				p.choice = i
				return m.handleSettings(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
		}
	case strings.HasPrefix(target, "collection:"):
		_, _ = fmt.Sscanf(target, "collection:%d", &p.collection.row)
		return m.collectionKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	case strings.HasPrefix(target, "cell:"):
		d := p.collection
		d.cells[d.cell] = p.input.Value()
		_, _ = fmt.Sscanf(target, "cell:%d", &d.cell)
		p.input.SetValue(d.cells[d.cell])
		return m, p.input.Focus()
	case strings.HasPrefix(target, "action:"):
		a := p.action
		var menu *interaction.CommandMenu
		if a.prompt != nil {
			menu = a.prompt.Menu
		} else if len(a.menus) > 0 {
			menu = a.menus[len(a.menus)-1]
		}
		if menu != nil {
			for i, c := range menu.Options {
				if target == "action:"+c.Label+":"+c.Arguments {
					a.choice = i
					return m.settingActionKey(tea.KeyPressMsg{Code: tea.KeyEnter})
				}
			}
		}
	}
	return m, nil
}
func (p *settingsPanel) footer() string {
	if p.editing != nil && p.editing.Kind == interaction.SettingInfo {
		return "↑↓ / PgUp PgDn scroll · Esc back"
	}
	if p.editing != nil {
		return "[Save] [Cancel] · Enter confirm · Esc back"
	}
	if p.usage {
		return "Tab category · ↑↓ select · r refresh · Esc close"
	}
	return "Tab category · ↑↓ · Enter edit · ? details · D default · u inherit · / search · Esc"
}
func (m model) settingsLauncher(mouse tea.Mouse) string {
	if m.inputContext().domain != inputMain || m.readSettings == nil {
		return ""
	}
	header := m.screenLayout().header
	if mouse.Y != header.y+1 {
		return ""
	}
	x := mouse.X - header.x - 1
	if x >= 0 && x < 10 {
		return "settings"
	}
	if x >= 12 && x < 19 && m.readUsage != nil {
		return "usage"
	}
	return ""
}
