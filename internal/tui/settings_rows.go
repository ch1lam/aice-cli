package tui

import (
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func (p *settingsPanel) visibleChoices() (start, end int) {
	height := max(1, p.layout.bodyHeight-2)
	start = max(0, p.choice-height+1)
	return start, min(len(p.editing.Choices), start+height)
}

func settingChoiceValue(field interaction.SettingField) string {
	if field.Value.Text == "" && field.Effective != "" {
		return field.Effective
	}
	return field.Value.Text
}

func settingRowValue(field interaction.SettingField) (value string, submenu bool) {
	value = settingValueText(field.Value, field.InvertBool)
	switch field.Kind {
	case interaction.SettingEnum:
		current := settingChoiceValue(field)
		if current != "" {
			value = current
		}
		for _, choice := range field.Choices {
			if choice.Value == current && choice.Label != "" {
				value = choice.Label
				break
			}
		}
		submenu = true
	case interaction.SettingAction:
		value, submenu = "", true
	case interaction.SettingInfo:
		value, submenu = field.Value.Text, true
	case interaction.SettingContexts, interaction.SettingList:
		submenu = true
	}
	return sanitizeSingleLineText(value), submenu
}

func settingRow(field interaction.SettingField, selected bool, width int) string {
	prefix, nameStyle := "  ", bodyStyle
	if selected {
		prefix, nameStyle = "› ", labelStyle
	}
	label := sanitizeSingleLineText(field.Label)
	if width < 6 {
		return nameStyle.Render(ansi.Truncate(prefix+label, width, "…"))
	}
	value, submenu := settingRowValue(field)
	valueStyle := bodyStyle
	if field.Source.Kind == "default" || (field.Kind == interaction.SettingBool && field.Value.Bool == field.InvertBool) {
		valueStyle = mutedStyle
	}
	// Reserve a shared right edge for values and a separate submenu arrow.
	// Long values leave at least half the available space for the label.
	value = ansi.Truncate(value, (width-6)/2, "…")
	label = ansi.Truncate(label, width-6-ansi.StringWidth(value), "…")
	gap := width - 4 - ansi.StringWidth(label) - ansi.StringWidth(value)
	arrow := "  "
	if submenu {
		arrow = " ›"
	}
	return nameStyle.Render(prefix+label) + strings.Repeat(" ", gap) +
		valueStyle.Render(value) + mutedStyle.Render(arrow)
}
