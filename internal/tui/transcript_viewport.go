package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// transcriptItem delays formatting until scrolling reaches this block. Version
// must be comparable and describe every input to render other than width.
type transcriptItem struct {
	key     int
	version any
	render  func() string
	gap     int
	lines   []string
	width   int
}

// transcriptViewport anchors scrolling to an item and a row inside it. It does
// not need to render preceding history to locate the visible window. All state,
// including cached wrapped lines, belongs to the TUI update/render loop.
type transcriptViewport struct {
	items                  []transcriptItem
	width, height          int
	index, line            int
	MouseWheelDelta        int
	itemsWidth             int
	resizing, resizeFollow bool
}

func newTranscriptViewport() transcriptViewport {
	return transcriptViewport{width: 80, height: 20, MouseWheelDelta: 3}
}

func (v *transcriptViewport) setItems(items []transcriptItem) {
	anchor, hasAnchor := 0, len(v.items) > 0 && v.index < len(v.items)
	if hasAnchor {
		anchor = v.items[v.index].key
	}
	nextIndex := min(v.index, max(len(items)-1, 0))
	anchorFound := false
	for i := range items {
		if hasAnchor && items[i].key == anchor {
			nextIndex = i
			anchorFound = true
		}
		if i < len(v.items) && v.itemsWidth == v.width {
			old := v.items[i]
			if old.key == items[i].key && old.version == items[i].version {
				items[i].lines, items[i].width = old.lines, old.width
			}
		}
	}
	v.items, v.itemsWidth, v.index, v.resizing = items, v.width, nextIndex, false
	if !anchorFound {
		v.line = 0
	}
	if len(items) > 0 && v.line > 0 {
		v.line = min(v.line, v.itemHeight(v.index)-1)
	}
}

func (v transcriptViewport) Width() int  { return v.width }
func (v transcriptViewport) Height() int { return v.height }
func (v *transcriptViewport) SetWidth(width int) {
	width = max(width, 1)
	if width != v.width {
		v.beginResize()
		v.width = width
	}
}
func (v *transcriptViewport) SetHeight(height int) {
	height = max(height, 1)
	if height != v.height {
		v.beginResize()
		v.height = height
	}
}
func (v *transcriptViewport) beginResize() {
	if !v.resizing {
		v.resizeFollow = v.AtBottom()
		v.resizing = true
	}
}

func (v transcriptViewport) itemLines(index int) []string {
	item := &v.items[index]
	if item.lines == nil || item.width != v.width {
		item.lines = wrapTranscriptLines(item.render(), v.width)
		item.width = v.width
	}
	return item.lines
}

func (v transcriptViewport) itemHeight(index int) int {
	return len(v.itemLines(index)) + v.items[index].gap
}

func (v transcriptViewport) AtBottom() bool {
	if v.resizing {
		return v.resizeFollow
	}
	remaining := -v.line
	for i := v.index; i < len(v.items); i++ {
		remaining += v.itemHeight(i)
		if remaining > v.height {
			return false
		}
	}
	return true
}

func (v *transcriptViewport) GotoTop() { v.index, v.line = 0, 0 }
func (v *transcriptViewport) GotoBottom() {
	remaining := v.height
	v.index, v.line = 0, 0
	for i := len(v.items) - 1; i >= 0; i-- {
		height := v.itemHeight(i)
		if height >= remaining {
			v.index, v.line = i, height-remaining
			return
		}
		remaining -= height
	}
}

// YOffset is a selection coordinate, not an exact scrollbar total. Unknown
// preceding blocks count as one row; measuring them here would defeat lazy
// rendering. Drag selection freezes both this coordinate and the visible rows.
func (v transcriptViewport) YOffset() int {
	offset := v.line
	for i := 0; i < v.index; i++ {
		offset += max(1, len(v.items[i].lines)) + v.items[i].gap
	}
	return offset
}

func (v *transcriptViewport) SetYOffset(offset int) {
	v.GotoTop()
	v.scroll(max(offset, 0))
}

func (v *transcriptViewport) scroll(delta int) {
	if len(v.items) == 0 {
		return
	}
	if delta < 0 {
		for delta < 0 {
			take := min(-delta, v.line)
			v.line -= take
			delta += take
			if delta == 0 || v.index == 0 {
				break
			}
			v.index--
			v.line = v.itemHeight(v.index)
		}
	} else {
		for delta > 0 {
			remaining := v.itemHeight(v.index) - v.line
			if delta < remaining {
				v.line += delta
				break
			}
			if v.index+1 >= len(v.items) {
				v.GotoBottom()
				return
			}
			delta -= remaining
			v.index++
			v.line = 0
		}
		if v.AtBottom() {
			v.GotoBottom()
		}
	}
}

func (v *transcriptViewport) PageUp()   { v.scroll(-v.height) }
func (v *transcriptViewport) PageDown() { v.scroll(v.height) }

func (v transcriptViewport) Update(msg tea.Msg) (transcriptViewport, tea.Cmd) {
	if mouse, ok := msg.(tea.MouseWheelMsg); ok {
		switch mouse.Button {
		case tea.MouseWheelUp:
			v.scroll(-v.MouseWheelDelta)
		case tea.MouseWheelDown:
			v.scroll(v.MouseWheelDelta)
		}
	}
	return v, nil
}

func (v transcriptViewport) View() string {
	lines := make([]string, 0, v.height)
	skip := v.line
	for i := v.index; i < len(v.items) && len(lines) < v.height; i++ {
		rows := v.itemLines(i)
		for j := 0; j < v.items[i].gap; j++ {
			if skip > 0 {
				skip--
				continue
			}
			if len(lines) < v.height {
				lines = append(lines, "")
			}
		}
		if skip >= len(rows) {
			skip -= len(rows)
			continue
		}
		count := min(len(rows)-skip, v.height-len(lines))
		lines = append(lines, rows[skip:skip+count]...)
		skip = 0
	}
	return lipgloss.NewStyle().Width(v.width).Height(v.height).Render(strings.Join(lines, "\n"))
}

// Full-content access is for explicit snapshots/tests, never a frame or scroll.
func (v transcriptViewport) GetContent() string {
	var lines []string
	for i := range v.items {
		for range v.items[i].gap {
			lines = append(lines, "")
		}
		lines = append(lines, v.itemLines(i)...)
	}
	return strings.Join(lines, "\n")
}

func (v *transcriptViewport) SetContent(content string) {
	v.setItems([]transcriptItem{{version: content, render: func() string { return content }}})
	if v.AtBottom() {
		v.GotoBottom()
	}
}

// Each wrapped row must carry its own styling: the viewport may start midway
// through a colored line. Hardwrap chooses grapheme-safe boundaries; Cut
// preserves the ANSI state when extracting each independently visible row.
func wrapTranscriptLines(content string, width int) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		wrapped := strings.Split(ansi.Hardwrap(line, width, true), "\n")
		if len(wrapped) == 1 {
			lines = append(lines, line)
			continue
		}
		offset := 0
		for _, row := range wrapped {
			end := offset + ansi.StringWidth(row)
			lines = append(lines, ansi.Cut(line, offset, end))
			offset = end
		}
	}
	return lines
}
