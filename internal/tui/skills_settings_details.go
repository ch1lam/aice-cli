package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Text is sanitized by the settings view before adding terminal styles.
func skillsSettingsDetails(text string, width int) string {
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		content := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(content)]
		rendered := bodyStyle.Render(line)
		if content == "Diagnostics" || content == "Notes" {
			rendered = labelStyle.Render(content)
		} else if key, value, ok := strings.Cut(content, ": "); ok {
			style := bodyStyle
			switch key {
			case "Skill":
				style = skillStyle.Bold(true)
			case "Source":
				style = labelStyle
			case "Location", "Install with":
				style = infoStyle
			case "Count":
				style = mutedStyle
			case "Severity":
				style = noticeStyle
				if value == "error" {
					style = errorStyle
				}
			}
			rendered = indent + mutedStyle.Render(key+": ") + style.Render(value)
		}
		wrapped := ansi.Wrap(rendered, max(1, width), "")
		rows = append(rows, wrapped)
	}
	return strings.Join(rows, "\n")
}
