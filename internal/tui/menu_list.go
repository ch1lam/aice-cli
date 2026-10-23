package tui

// Selectable lists share one cursor mark, movement rule and scrolling window.
// Each list keeps its own row styling.

// menuPrefix is the cursor column of one list row.
func menuPrefix(selected bool) string {
	if selected {
		return "› "
	}
	return "  "
}

// wrapIndex moves a cyclic selection; an empty list stays at zero.
func wrapIndex(index, delta, count int) int {
	if count <= 0 {
		return 0
	}
	return ((index+delta)%count + count) % count
}

// clampIndex keeps a selection on an existing row; an empty list stays at zero.
func clampIndex(index, count int) int {
	return max(0, min(index, count-1))
}

// listWindow returns the visible rows [start, end) of a list scrolled just far
// enough to keep the selection on its last row.
func listWindow(selection, height, count int) (start, end int) {
	start = max(0, selection-height+1)
	return start, min(count, start+height)
}
