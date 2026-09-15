package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Text and its interactive geometry travel together through composition and
// caching. Coordinates are terminal cells relative to this content's origin.
type transcriptContent struct {
	view   string
	blocks []codeBlockPlacement
}

type codeBlockPlacement struct {
	layout      codeBlockLayout
	row, column int
}

func (l codeBlockLayout) content() transcriptContent {
	return transcriptContent{view: l.view(), blocks: []codeBlockPlacement{{layout: l}}}
}

func (c *transcriptContent) append(part transcriptContent, separator string) {
	if part.view == "" {
		return
	}
	if c.view != "" {
		c.view += separator
	}
	row := strings.Count(c.view, "\n")
	for _, block := range part.blocks {
		block.row += row
		c.blocks = append(c.blocks, block)
	}
	c.view += part.view
}

func (c *transcriptContent) appendText(text string) {
	c.append(transcriptContent{view: text}, "\n")
}

func (c transcriptContent) pad(left, right int) transcriptContent {
	blocks := make([]codeBlockPlacement, len(c.blocks))
	for i, block := range c.blocks {
		block.column += left
		blocks[i] = block
	}
	c.blocks = blocks
	c.view = lipgloss.NewStyle().PaddingLeft(left).PaddingRight(right).Render(c.view)
	return c
}

// Code panels already wrap at their own width. Wrap only surrounding text;
// reflowing a panel here would separate its hit targets from its painted rows.
func (c transcriptContent) wrapText(width int) transcriptContent {
	var out transcriptContent
	var view strings.Builder
	rows := strings.Split(c.view, "\n")
	blockIndex, outputRow := 0, 0
	for row := 0; row < len(rows); {
		if row > 0 {
			view.WriteByte('\n')
			outputRow++
		}
		if blockIndex < len(c.blocks) && c.blocks[blockIndex].row == row {
			block := c.blocks[blockIndex]
			end := row + len(block.layout.rows)
			block.row = outputRow
			out.blocks = append(out.blocks, block)
			view.WriteString(strings.Join(rows[row:end], "\n"))
			outputRow += end - row - 1
			row, blockIndex = end, blockIndex+1
		} else {
			wrapped := ansi.Wrap(rows[row], width, "")
			view.WriteString(wrapped)
			outputRow += strings.Count(wrapped, "\n")
			row++
		}
	}
	out.view = view.String()
	return out
}

func (m model) indentTranscript(content transcriptContent) transcriptContent {
	return content.wrapText(m.contentWidth()).pad(transcriptContentIndent, 1)
}
