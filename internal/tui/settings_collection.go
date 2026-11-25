package tui

import (
	"fmt"
	"slices"
	"strings"

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
	start, end := listWindow(d.row, p.layout.bodyHeight-4, len(rows))
	var visible []string
	for i := start; i < end; i++ {
		visible = append(visible, sanitizeSingleLineText(menuPrefix(i == d.row)+rows[i]))
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
	return strings.Join(visible, "\n") + "\n" + catalog
}
