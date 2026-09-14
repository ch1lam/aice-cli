package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m model) contextFraction() string {
	usage := m.contextUsage
	used, window := "?", "?"
	if usage.Known {
		used = formatContextTokens(max(usage.Tokens, 0))
	}
	if usage.Window > 0 {
		window = formatContextTokens(usage.Window)
	}
	return used + " / " + window
}

func formatContextTokens(tokens int64) string {
	divisor, suffix := float64(1), ""
	switch {
	case tokens >= 1_000_000:
		divisor, suffix = 1_000_000, "M"
	case tokens >= 1_000:
		divisor, suffix = 1_000, "K"
	default:
		return strconv.FormatInt(tokens, 10)
	}
	value := strconv.FormatFloat(float64(tokens)/divisor, 'f', 1, 64)
	return strings.TrimSuffix(value, ".0") + suffix
}

// Reserve both representations so hovering never changes wrapping or hit bounds.
func (m model) contextHeaderWidth() int {
	if m.contextUsage == (DisplayContext{}) {
		return 0
	}
	return max(lipgloss.Width(m.contextStatus()), lipgloss.Width(m.contextFraction()))
}

func (m model) contextContains(mouse tea.Mouse) bool {
	if m.guardPending != nil || m.contextHeaderWidth() == 0 {
		return false
	}
	right := m.horizontalPadding() + m.layoutWidth() - 1
	return mouse.Y == m.verticalPadding() &&
		mouse.X >= right-m.contextHeaderWidth() && mouse.X < right
}

// A click accepts the hover preview once; keep it until the pointer leaves.
func (m model) contextFractionVisible() bool {
	hovered := m.pointer.known && !m.selection.active &&
		m.contextContains(tea.Mouse{X: m.pointer.x, Y: m.pointer.y})
	preview := hovered && !m.contextHoverConfirmed
	return m.contextShowFraction != preview
}

func (m model) contextHeaderView() string {
	if m.contextFractionVisible() {
		return bodyStyle.Render(m.contextFraction())
	}
	return m.contextStatus()
}
