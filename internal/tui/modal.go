package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type modalLayout struct{ x, y, width, height, inner, bodyHeight int }

func centeredModal(width, height, maxWidth, maxHeight int) modalLayout {
	w, h := max(8, min(width-4, maxWidth)), max(8, min(height-2, maxHeight))
	return modalLayout{x: max(0, (width-w)/2), y: max(0, (height-h)/2), width: w, height: h, inner: w - 4, bodyHeight: h - 5}
}

func modalTopBorder(title string, inner int, hovered, pressed bool) string {
	style := mutedStyle.Background(inkBlackColor)
	if hovered {
		style = style.Foreground(errorColor).Bold(pressed)
	}
	titleWidth := max(0, inner-ansi.StringWidth(sessionPickerCloseLabel)-1)
	heading := ansi.Truncate(" "+sanitizeSingleLineText(title)+" ", titleWidth, "…")
	gap := max(0, inner-ansi.StringWidth(heading)-ansi.StringWidth(sessionPickerCloseLabel))
	border := slashCommandMenuStyle.GetBorderStyle()
	stroke := lipgloss.NewStyle().Foreground(slashCommandMenuStyle.GetBorderTopForeground()).Background(inkBlackColor)
	return stroke.Render(border.TopLeft+border.Top) + bodyStyle.Bold(true).Background(inkBlackColor).Render(heading) + stroke.Render(strings.Repeat(border.Top, gap)) + style.Render(sessionPickerCloseLabel) + stroke.Render(border.Top+border.TopRight)
}

func modalFrame(title, content string, l modalLayout, hovered, pressed bool) string {
	frame := slashCommandMenuStyle.Foreground(primaryTextColor).Background(inkBlackColor).BorderBackground(inkBlackColor).Width(l.width).Render(content)
	_, rest, _ := strings.Cut(frame, "\n")
	return modalTopBorder(title, l.inner, hovered, pressed) + "\n" + rest
}

func composeModal(content, frame string, width, height int, l modalLayout) string {
	return lipgloss.NewCanvas(width, height).Compose(lipgloss.NewCompositor(lipgloss.NewLayer(content), lipgloss.NewLayer(restoreCanvasColors(frame)).X(l.x).Y(l.y).Z(1))).Render()
}

func modalCloseContains(l modalLayout, mouse tea.Mouse) bool {
	x, y := mouse.X-l.x-2, mouse.Y-l.y
	return y == 0 && x >= l.inner-ansi.StringWidth(sessionPickerCloseLabel) && x < l.inner
}
