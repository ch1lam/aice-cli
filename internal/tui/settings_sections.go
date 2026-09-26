package tui

import (
	"strings"

	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

// Sections are a presentation of the existing field IDs, not new settings or
// persistence boundaries. Unknown fields remain visible in General.
func settingSection(field interaction.SettingField) string {
	id := field.ID
	switch field.Category {
	case "models":
		switch {
		case strings.HasPrefix(id, "account."):
			return "Accounts"
		case strings.HasSuffix(id, "_base_url"):
			return "Provider endpoints"
		default:
			return "Model & reasoning"
		}
	case "tools":
		switch {
		case strings.HasPrefix(id, "browser"):
			return "Browser"
		case strings.HasPrefix(id, "desktop"):
			return "Computer Use"
		case strings.HasPrefix(id, "web.service."):
			instance := strings.TrimPrefix(id, "web.service.")
			if end := strings.LastIndexByte(instance, '.'); end >= 0 {
				return "Search service · " + instance[:end]
			}
		case strings.HasPrefix(id, "web.fetch."):
			return "Web fetch"
		case strings.HasPrefix(id, "web."):
			return "Web search"
		}
	case "limits":
		switch id {
		case "run_no_progress_limit":
			return "Progress detection"
		default:
			return "Run budgets"
		}
	case "project":
		switch id {
		case "default_project_trust", "project.trust", "project.loaded":
			return "Trust"
		default:
			return "Workspace"
		}
	case "system":
		switch {
		case strings.HasPrefix(id, "system.diagnostic."):
			return "Diagnostics"
		case strings.HasPrefix(id, "system."):
			return "Configuration files"
		default:
			return "Startup"
		}
	case "context":
		return "Context"
	case "usage":
		return "Session usage"
	case "session":
		return "Session information"
	}
	return "General"
}

func groupSettingFields(fields []interaction.SettingField) []interaction.SettingField {
	var groups [][]interaction.SettingField
	indices := make(map[string]int)
	for _, field := range fields {
		key := field.Category + "/" + settingSection(field)
		index, ok := indices[key]
		if !ok {
			index = len(groups)
			indices[key] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], field)
	}
	result := make([]interaction.SettingField, 0, len(fields))
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

type settingListRow struct {
	field   int // -1 for a heading or blank separator; never selectable.
	section string
}

func (p *settingsPanel) listSize() (width, height int) {
	if p.layout.inner >= 88 {
		return p.layout.inner/2 - 2, p.layout.bodyHeight
	}
	return p.layout.inner, max(1, p.layout.bodyHeight-5)
}

func (p *settingsPanel) sectionTitle(field interaction.SettingField) string {
	title := settingSection(field)
	if p.search && strings.TrimSpace(p.input.Value()) != "" {
		for _, category := range p.snapshot.Categories {
			if category.ID == field.Category && category.Label != title {
				return category.Label + " · " + title
			}
		}
	}
	return title
}

// Rendering and hit testing share these rows so headings cannot become fields.
// When a long section scrolls, repeat its heading above the visible fields.
func (p *settingsPanel) visibleSettingRows(fields []interaction.SettingField) []settingListRow {
	_, height := p.listSize()
	var rows []settingListRow
	section, selectedRow := "", 0
	for i, field := range fields {
		title := p.sectionTitle(field)
		if title != section {
			if len(rows) > 0 {
				rows = append(rows, settingListRow{field: -1})
			}
			rows = append(rows, settingListRow{field: -1, section: title})
			section = title
		}
		if i == p.selection {
			selectedRow = len(rows)
		}
		rows = append(rows, settingListRow{field: i, section: title})
	}
	start := max(0, selectedRow-height+1)
	if start > 0 && height > 1 {
		// Reserve one row for the heading if the window starts within a section.
		if rows[start].field >= 0 || rows[start].section == "" {
			start++
			if rows[start].field >= 0 {
				visible := []settingListRow{{field: -1, section: rows[start].section}}
				return append(visible, rows[start:min(len(rows), start+height-1)]...)
			}
		}
	}
	return rows[start:min(len(rows), start+height)]
}

func settingSectionHeading(title string, width int) string {
	if title == "" {
		return ""
	}
	label := ansi.Truncate("  "+sanitizeSingleLineText(title)+" ", width, "…")
	return mutedStyle.Bold(true).Render(label) +
		mutedStyle.Render(strings.Repeat("─", max(0, width-ansi.StringWidth(label))))
}

func (p *settingsPanel) tabsWidth() int {
	width := max(0, len(p.snapshot.Categories)-1) * 3
	for _, tab := range p.snapshot.Categories {
		width += ansi.StringWidth(tab.Label)
	}
	return width
}
