package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Style only sanitized display text; app status remains frontend-neutral.
func mcpSettingsDetails(text string, width int) string {
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		var rendered string
		switch line {
		case "Connection", "Tool catalog", "Permissions", "Diagnostics", "Notes", "Service configuration", "Connection authorization",
			"Credentials and login", "Operation permissions", "Computer Use":
			rendered = labelStyle.Render(line)
		default:
			key, value, found := strings.Cut(line, ": ")
			if !found {
				rendered = mutedStyle.Render(line)
				break
			}
			style := bodyStyle
			switch key {
			case "Service":
				style = headerStyle
			case "Source", "Fingerprint", "Connection fingerprint", "Schema fingerprint", "Saved rule binding":
				style = infoStyle
			case "State", "Approval":
				switch value {
				case "ready", "allow":
					style = skillStyle
				case "failed", "deny":
					style = errorStyle
				case "needs_auth", "connecting", "ask":
					style = noticeStyle
				default:
					style = mutedStyle
				}
			}
			rendered = mutedStyle.Render(key+": ") + style.Render(value)
		}
		rows = append(rows, ansi.Wrap(rendered, max(1, width), ""))
	}
	return strings.Join(rows, "\n")
}
