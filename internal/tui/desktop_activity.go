package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

// Keep one current presentation row. It never polls native state or retains
// references capable of executing input; the application projects those facts.
func (m *model) setDesktopActivity(display *interaction.DesktopDisplay, completed, failed bool) {
	wasVisible := m.desktopActivity != nil
	m.desktopActivity = nil
	if display != nil {
		copy := *display
		if completed && !failed && (copy.Phase == "Returned" || copy.Phase == "Condition met") {
			copy.Phase = "Planning"
		}
		m.desktopActivity = &copy
	}
	if wasVisible != (m.desktopActivity != nil) {
		m.resizeLayout()
	}
}

func desktopActivityLabel(display interaction.DesktopDisplay, width int) string {
	phase := sanitizeSingleLineText(display.Phase)
	base := "Computer Use · "
	if ansi.StringWidth(base+phase) > width {
		return ansi.Truncate(phase, max(width, 1), "…")
	}
	app := sanitizeSingleLineText(display.App)
	// Prefer the real state over a long application name on narrow terminals.
	available := width - ansi.StringWidth(base+phase) - 3
	if app != "" && available >= 4 {
		return base + ansi.Truncate(app, available, "…") + " · " + phase
	}
	return ansi.Truncate(base+phase, max(width, 1), "…")
}

func (m model) desktopActivityView(width int) string {
	if !m.running || m.desktopActivity == nil {
		return ""
	}
	display := *m.desktopActivity
	if m.cancelRequested {
		display.Phase = "Stopping"
	} else if display.Phase == "Planning" && strings.HasPrefix(m.status, "Retrying") {
		display.Phase = "Waiting for model"
	}
	return mutedStyle.Render(desktopActivityLabel(display, width))
}
