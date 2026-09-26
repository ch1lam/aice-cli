package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
)

// collectionDraft owns an array as one atomic field, with a small row editor.
type collectionDraft struct {
	value                           interaction.SettingValue
	row, cell                       int
	cells                           []string
	editing, adding, dirty, leaving bool
}

func (d *collectionDraft) count() int {
	if d.value.Kind == interaction.SettingContexts {
		return len(d.value.Contexts)
	}
	return len(d.value.List)
}
func (d *collectionDraft) rows() []string {
	if d.value.Kind == interaction.SettingList {
		return d.value.List
	}
	var rows []string
	for _, c := range d.value.Contexts {
		rows = append(rows, fmt.Sprintf("%s / %s   %d tokens", c.Provider, c.Model, c.Tokens))
	}
	return rows
}
func (m model) collectionKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.settings
	d := p.collection
	name := key.String()
	if d.leaving {
		switch name {
		case "esc", "k":
			d.leaving = false
			p.notice = "Draft retained"
		case "d":
			p.collection = nil
			p.editing = nil
			p.input.SetValue("")
			p.input.Blur()
			p.notice = "Draft discarded"
		case "s":
			d.leaving = false
			return m, m.submitSetting(interaction.SettingChange{ID: p.editing.ID, Value: d.value})
		}
		return m, nil
	}
	if d.editing {
		if name == "esc" {
			d.editing = false
			p.input.Blur()
			p.input.SetValue("")
			return m, nil
		}
		if name == "tab" || name == "shift+tab" {
			d.cells[d.cell] = p.input.Value()
			delta := 1
			if name == "shift+tab" {
				delta = -1
			}
			d.cell = (d.cell + delta + len(d.cells)) % len(d.cells)
			p.input.SetValue(d.cells[d.cell])
			p.input.CursorEnd()
			return m, nil
		}
		if name == "enter" {
			d.cells[d.cell] = p.input.Value()
			if d.value.Kind == interaction.SettingContexts {
				tokens, err := strconv.ParseInt(strings.TrimSpace(d.cells[2]), 10, 64)
				if err != nil || tokens <= 0 {
					p.notice = "Tokens must be a positive integer"
					return m, nil
				}
				entry := interaction.ContextWindowSetting{Provider: strings.TrimSpace(d.cells[0]), Model: strings.TrimSpace(d.cells[1]), Tokens: tokens}
				if entry.Provider == "" || entry.Model == "" {
					p.notice = "Provider and model are required"
					return m, nil
				}
				if d.adding {
					d.value.Contexts = append(d.value.Contexts, entry)
				} else {
					d.value.Contexts[d.row] = entry
				}
			} else {
				entry := strings.TrimSpace(d.cells[0])
				if entry == "" {
					p.notice = "An entry cannot be blank; delete the row to remove it"
					return m, nil
				}
				if d.adding {
					d.value.List = append(d.value.List, entry)
				} else {
					d.value.List[d.row] = entry
				}
			}
			d.dirty = true
			d.editing = false
			p.input.Blur()
			p.input.SetValue("")
			p.notice = "Row updated in draft · Ctrl+S saves the whole list"
			return m, nil
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(key)
		return m, m.scopeInputCommand(cmd)
	}
	switch name {
	case "esc":
		if d.dirty {
			d.leaving = true
			p.notice = "Unsaved list: s save · d discard · k / Esc keep editing"
		} else {
			p.collection = nil
			p.editing = nil
		}
		return m, nil
	case "ctrl+s":
		return m, m.submitSetting(interaction.SettingChange{ID: p.editing.ID, Value: d.value})
	case "up":
		d.row = max(0, d.row-1)
	case "down":
		d.row = min(max(0, d.count()-1), d.row+1)
	case "ctrl+up", "ctrl+down":
		other := d.row - 1
		if name == "ctrl+down" {
			other = d.row + 1
		}
		if other >= 0 && other < d.count() {
			if d.value.Kind == interaction.SettingList {
				d.value.List[d.row], d.value.List[other] = d.value.List[other], d.value.List[d.row]
			} else {
				d.value.Contexts[d.row], d.value.Contexts[other] = d.value.Contexts[other], d.value.Contexts[d.row]
			}
			d.row = other
			d.dirty = true
		}
	case "d", "delete":
		if d.count() > 0 {
			if d.value.Kind == interaction.SettingList {
				d.value.List = slices.Delete(d.value.List, d.row, d.row+1)
			} else {
				d.value.Contexts = slices.Delete(d.value.Contexts, d.row, d.row+1)
			}
			d.row = min(d.row, max(0, d.count()-1))
			d.dirty = true
		}
	case "a", "enter":
		d.adding = name == "a" || d.count() == 0
		d.cell = 0
		d.cells = []string{""}
		if d.value.Kind == interaction.SettingContexts {
			d.cells = []string{"", "", ""}
			if !d.adding {
				v := d.value.Contexts[d.row]
				d.cells = []string{v.Provider, v.Model, strconv.FormatInt(v.Tokens, 10)}
			}
		} else if !d.adding {
			d.cells[0] = d.value.List[d.row]
		}
		d.editing = true
		p.input.SetValue(d.cells[0])
		p.notice = "Tab changes cell · Enter keeps row · Esc cancels row"
		return m, p.input.Focus()
	}
	return m, nil
}
func (p *settingsPanel) collectionView() string {
	d := p.collection
	if d.editing {
		labels := []string{"Entry"}
		if d.value.Kind == interaction.SettingContexts {
			labels = []string{"Provider ID", "Model ID (exact)", "Tokens"}
		}
		var rows []string
		for i, label := range labels {
			value := d.cells[i]
			if i == d.cell {
				value = p.input.View()
			}
			rows = append(rows, label+"\n"+value)
		}
		catalog := ""
		if len(d.cells) == 3 {
			values := slices.Clone(d.cells)
			values[d.cell] = p.input.Value()
			for _, c := range p.editing.Choices {
				if c.Value == values[0]+"/"+values[1] {
					catalog = "\nCatalog capacity: " + c.Label
					break
				}
			}
		}
		return strings.Join(rows, "\n") + catalog + "\n\n" + sanitizeMultilineText(p.editing.Description)
	}
	rows := d.rows()
	start := max(0, d.row-p.layout.bodyHeight+5)
	var visible []string
	for i := start; i < min(len(rows), start+p.layout.bodyHeight-4); i++ {
		prefix := "  "
		if i == d.row {
			prefix = "› "
		}
		visible = append(visible, sanitizeSingleLineText(prefix+rows[i]))
	}
	if len(rows) == 0 {
		visible = append(visible, "Empty list")
	}
	for len(visible) < max(1, p.layout.bodyHeight-4) {
		visible = append(visible, "")
	}
	catalog := ""
	if len(p.editing.Choices) > 0 {
		catalog = "Catalog defaults shown in the row editor and field details"
	}
	return strings.Join(visible, "\n") + "\n[Add] [Delete] [↑] [↓]\nCtrl+S Save · Esc Cancel\n" + catalog
}
