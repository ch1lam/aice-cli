package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Match the user and assistant text column. Fold depth changes visibility,
// never the left edge; wrap before padding so continuation rows align too.
const transcriptContentIndent = 3

func (m model) userMessageView(text string) string {
	return userStyle.Width(m.layoutWidth()).Render(text)
}

func (m model) transcriptContentView(content string) string {
	return lipgloss.NewStyle().PaddingLeft(transcriptContentIndent).PaddingRight(1).
		Render(ansi.Wrap(content, m.contentWidth(), ""))
}

// Reserve breathing room around the entire interface. Small terminals reclaim
// it so the existing minimum content width and compact controls still fit.
func (m model) horizontalPadding() int {
	return min(2, max((m.width-minimumWidth)/2, 0))
}

func (m model) verticalPadding() int {
	if m.height < 12 {
		return 0
	}
	return 1
}

func (m model) layoutWidth() int {
	return max(m.width-2*m.horizontalPadding(), minimumWidth)
}

func (m model) layoutHeight() int {
	return m.height - 2*m.verticalPadding()
}

// screenRect uses terminal cells and half-open bounds. Transcript internals
// remain lazy; only the surrounding chrome is measured during resizeLayout.
type screenRect struct{ x, y, width, height int }

func (r screenRect) contains(mouse tea.Mouse) bool {
	return mouse.X >= r.x && mouse.X < r.x+r.width && mouse.Y >= r.y && mouse.Y < r.y+r.height
}

type chromeMeasurements struct{ header, menu, composer, footer int }
type screenLayout struct{ header, transcript, menu, composer, footer screenRect }

func (m model) screenLayout() screenLayout {
	x, y, width := m.horizontalPadding(), m.verticalPadding(), m.layoutWidth()
	c := m.chrome
	header := screenRect{x, y, width, c.header}
	transcript := screenRect{x, y + c.header, m.viewport.Width(), m.viewport.Height()}
	menu := screenRect{x, transcript.y + transcript.height, width, c.menu}
	composer := screenRect{x, menu.y + menu.height, width, c.composer}
	footer := screenRect{x, composer.y + composer.height, width, c.footer}
	return screenLayout{header, transcript, menu, composer, footer}
}
