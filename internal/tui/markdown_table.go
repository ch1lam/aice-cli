package tui

import (
	"bytes"
	"strings"

	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
	astext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Grok-style tables: a full outer box (┌─┬─┐), dividers between every body
// row (├─┼─┤), dimmed borders and a bold header. Glamour disables the outer
// border, leaving only inner column/row lines, so tables are extracted into
// placeholders like code blocks and laid out here with lipgloss. The frame
// keeps AICE's own palette (separator borders, primary text); only the box
// idea comes from Grok. Cell inline content (code, emphasis, links) still
// renders through Glamour's own element builders, mirroring TableCellElement.
type tableBlock struct {
	node   *astext.Table
	source []byte
}

// extractMarkdownTables replaces every table with a collision-free
// single-cell code placeholder, exactly like code blocks, so Glamour lays out
// the tree (and its block order) untouched. The returned source carries the
// appended markers; earlier cell ranges stay valid.
func extractMarkdownTables(document ast.Node, source []byte) ([]tableBlock, string, []byte, error) {
	var nodes []*astext.Table
	_ = ast.Walk(document, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if tbl, ok := n.(*astext.Table); ok {
				nodes = append(nodes, tbl)
			}
		}
		return ast.WalkContinue, nil
	})
	if len(nodes) == 0 {
		return nil, "", source, nil
	}
	// The appended code markers count as used, so this marker differs too.
	marker, err := markdownMarker(string(source))
	if err != nil {
		return nil, "", source, err
	}
	tables := make([]tableBlock, 0, len(nodes))
	for _, node := range nodes {
		placeholder := ast.NewCodeBlock()
		start := len(source)
		source = append(source, marker...)
		source = append(source, '\n')
		placeholder.Lines().Append(text.NewSegment(start, len(source)))
		node.Parent().ReplaceChild(node.Parent(), node, placeholder)
		tables = append(tables, tableBlock{node: node})
	}
	for i := range tables {
		tables[i].source = source
	}
	return tables, marker, source, nil
}

// isTablePlaceholder reports whether a post-extraction code node is a table
// slot rather than a code slot.
func isTablePlaceholder(node ast.Node, source []byte, tableMarker string) bool {
	if tableMarker == "" {
		return false
	}
	code, ok := node.(*ast.CodeBlock)
	if !ok {
		return false
	}
	for i := 0; i < code.Lines().Len(); i++ {
		line := code.Lines().At(i)
		if strings.Contains(string(line.Value(source)), tableMarker) {
			return true
		}
	}
	return false
}

// countMarkdownPlaceholders splits a part's code-looking nodes into code and
// table slots. Table placeholders share the code node kind.
func countMarkdownPlaceholders(first, end ast.Node, source []byte, tableMarker string) (code, tables int) {
	for node := first; node != end; node = node.NextSibling() {
		_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering && n.Kind() == ast.KindCodeBlock {
				if isTablePlaceholder(n, source, tableMarker) {
					tables++
				} else {
					code++
				}
			}
			return ast.WalkContinue, nil
		})
	}
	return code, tables
}

func renderBoxTable(tbl *astext.Table, source []byte, width int, styles glamouransi.StyleConfig) string {
	var header []string
	var rows [][]string
	helper := glamouransi.NewRenderer(glamouransi.Options{Styles: styles, WordWrap: width})
	cellContext := glamouransi.NewRenderContext(glamouransi.Options{Styles: styles, WordWrap: width})
	tableStyle := styles.Table.StylePrimitive
	for child := tbl.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case astext.KindTableHeader:
			// Header cells hang directly under the header node.
			for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if cell.Kind() != astext.KindTableCell {
					continue
				}
				header = append(header, renderBoxCell(cell, source, helper, cellContext, tableStyle))
			}
		case astext.KindTableRow:
			rows = append(rows, renderBoxRowCells(child, source, helper, cellContext, tableStyle))
		}
	}
	columns := len(header)
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	if columns == 0 {
		return ""
	}
	header = padBoxRow(header, columns)
	for i := range rows {
		rows[i] = padBoxRow(rows[i], columns)
	}

	t := buildBoxTable(header, rows, tbl.Alignments, styles)
	out := t.String()
	// Compact by default: keep the natural content width and only shrink
	// to the available width when the table would overflow.
	if boxDisplayWidth(out) > width {
		t.Width(max(width, columns*4+1))
		out = t.String()
	}
	return out
}

func buildBoxTable(header []string, rows [][]string, alignments []astext.Alignment, styles glamouransi.StyleConfig) *table.Table {
	t := table.New().
		Wrap(true).
		Border(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderRight(true).
		BorderHeader(true).
		BorderColumn(true).
		BorderRow(true).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(separatorHex))).
		StyleFunc(boxTableStyleFunc(alignments))
	if styles.Document.BackgroundColor != nil {
		t.BaseStyle(lipgloss.NewStyle().Background(lipgloss.Color(*styles.Document.BackgroundColor)))
	}
	t.Headers(header...)
	for _, row := range rows {
		t.Row(row...)
	}
	return t
}

func boxDisplayWidth(out string) int {
	width := 0
	for line := range strings.SplitSeq(out, "\n") {
		width = max(width, ansi.StringWidth(ansi.Strip(line)))
	}
	return width
}

func renderBoxRowCells(row ast.Node, source []byte, helper *glamouransi.ANSIRenderer, cellContext glamouransi.RenderContext, tableStyle glamouransi.StylePrimitive) []string {
	var cells []string
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		if cell.Kind() != astext.KindTableCell {
			continue
		}
		cells = append(cells, renderBoxCell(cell, source, helper, cellContext, tableStyle))
	}
	return cells
}

// Same cell body Glamour would place in its borderless table: each direct
// child renders through its own element builder into one buffer.
func renderBoxCell(cell ast.Node, source []byte, helper *glamouransi.ANSIRenderer, cellContext glamouransi.RenderContext, tableStyle glamouransi.StylePrimitive) string {
	var out bytes.Buffer
	for child := cell.FirstChild(); child != nil; child = child.NextSibling() {
		element := helper.NewElement(child, source)
		if element.Renderer == nil {
			continue
		}
		if overrider, ok := element.Renderer.(glamouransi.StyleOverriderElementRenderer); ok {
			_ = overrider.StyleOverrideRender(&out, cellContext, tableStyle)
			continue
		}
		var rendered bytes.Buffer
		if err := element.Renderer.Render(&rendered, cellContext); err != nil {
			continue
		}
		base := &glamouransi.BaseElement{Token: rendered.String(), Style: tableStyle}
		_ = base.Render(&out, cellContext)
	}
	return strings.TrimSpace(strings.ReplaceAll(out.String(), "\n", " "))
}

func padBoxRow(row []string, columns int) []string {
	padded := make([]string, columns)
	copy(padded, row)
	return padded
}

// Alignment matches Glamour's table StyleFunc; the header row additionally
// renders bold. No foreground is set so inline code/link colors inside cells
// keep coming from the cell content itself.
func boxTableStyleFunc(alignments []astext.Alignment) table.StyleFunc {
	return func(row, col int) lipgloss.Style {
		style := lipgloss.NewStyle().Inline(false).Margin(0, 1)
		var alignment astext.Alignment = astext.AlignNone
		if col >= 0 && col < len(alignments) {
			alignment = alignments[col]
		}
		switch alignment {
		case astext.AlignLeft:
			style = style.Align(lipgloss.Left).PaddingRight(0)
		case astext.AlignCenter:
			style = style.Align(lipgloss.Center)
		case astext.AlignRight:
			style = style.Align(lipgloss.Right).PaddingLeft(0)
		case astext.AlignNone:
			// Keep the default alignment.
		}
		if row == table.HeaderRow {
			style = style.Bold(true)
		}
		return style
	}
}
