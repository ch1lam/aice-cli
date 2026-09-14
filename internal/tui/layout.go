package tui

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
