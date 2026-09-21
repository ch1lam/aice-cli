package httpfetch

import (
	"bytes"
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/ch1lam/aice-cli/internal/web"
)

type extraction struct {
	title    string
	text     string
	method   string
	warnings []string
}

// extract turns a decoded body into model-facing text. Plain and Markdown
// bodies pass through; HTML is parsed into a DOM (never regex-stripped), noise
// elements are removed and the main/article/body content is converted.
func extract(ctx context.Context, body []byte, mediaType string, format web.FetchFormat, page *url.URL) (extraction, error) {
	switch mediaType {
	case "text/plain":
		return extraction{text: normalizeNewlines(string(body)), method: "text"}, nil
	case "text/markdown":
		return extraction{text: normalizeNewlines(string(body)), method: "markdown"}, nil
	}
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return extraction{}, web.NewError(web.CodeInvalidResponse, "html parse failed: %v", err)
	}
	title := strings.Join(strings.Fields(textOf(findFirst(document, atom.Title))), " ")
	root, method := selectRoot(document)
	converter := &converter{ctx: ctx, format: format, page: page, skipHeader: method == "body"}
	if err := converter.walk(root, walkState{}); err != nil {
		return extraction{}, err
	}
	text := strings.TrimSpace(collapseBlankLines(converter.out.String()))
	result := extraction{title: title, text: text, method: method}
	if text == "" {
		result.warnings = append(result.warnings, "no readable text was extracted from the HTML body")
	}
	return result, nil
}

func selectRoot(document *html.Node) (*html.Node, string) {
	if node := findFirst(document, atom.Main); node != nil {
		return node, "main"
	}
	if node := findFirst(document, atom.Article); node != nil {
		return node, "article"
	}
	if node := findFirst(document, atom.Body); node != nil {
		return node, "body"
	}
	return document, "document"
}

func findFirst(node *html.Node, tag atom.Atom) *html.Node {
	if node.Type == html.ElementNode && node.DataAtom == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func textOf(node *html.Node) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			builder.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return builder.String()
}

// skippedElements never contribute text: executable content, embedded frames
// and obvious page chrome.
var skippedElements = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Iframe: true, atom.Object: true,
	atom.Embed: true, atom.Svg: true, atom.Canvas: true, atom.Template: true, atom.Nav: true,
	atom.Aside: true, atom.Footer: true, atom.Head: true, atom.Title: true, atom.Link: true, atom.Meta: true,
}

type walkState struct {
	listDepth int
}

type converter struct {
	ctx        context.Context
	format     web.FetchFormat
	page       *url.URL
	skipHeader bool
	out        strings.Builder
	nodes      *int
	// pendingSpace collapses inline whitespace between text runs.
	pendingSpace bool
}

const nodeCheckInterval = 512

func (c *converter) markdown() bool { return c.format == web.FetchFormatMarkdown }

func (c *converter) child() *converter {
	if c.nodes == nil {
		c.nodes = new(int)
	}
	return &converter{ctx: c.ctx, format: c.format, page: c.page, skipHeader: c.skipHeader, nodes: c.nodes}
}

func (c *converter) walk(node *html.Node, state walkState) error {
	if c.nodes == nil {
		c.nodes = new(int)
	}
	*c.nodes++
	if *c.nodes%nodeCheckInterval == 0 {
		if err := c.ctx.Err(); err != nil {
			return web.WrapContextError(err, web.CodeCanceled, "html conversion interrupted")
		}
	}
	switch node.Type {
	case html.TextNode:
		c.writeText(node.Data)
		return nil
	case html.CommentNode, html.DoctypeNode:
		return nil
	case html.DocumentNode:
		return c.children(node, state)
	}
	if skippedElements[node.DataAtom] || hasAttr(node, "hidden") || (c.skipHeader && node.DataAtom == atom.Header) {
		return nil
	}
	switch node.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(node.Data[1] - '0')
		c.block()
		if c.markdown() {
			c.out.WriteString(strings.Repeat("#", level) + " ")
		}
		if err := c.children(node, state); err != nil {
			return err
		}
		c.block()
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Figure, atom.Figcaption, atom.Address, atom.Details, atom.Summary, atom.Dl, atom.Dt, atom.Dd, atom.Form, atom.Fieldset:
		c.block()
		if err := c.children(node, state); err != nil {
			return err
		}
		c.block()
	case atom.Br:
		c.pendingSpace = false
		c.out.WriteString("\n")
	case atom.Hr:
		c.block()
		if c.markdown() {
			c.out.WriteString("---")
		}
		c.block()
	case atom.Pre:
		c.block()
		content := strings.TrimRight(textOf(node), "\n")
		if c.markdown() {
			fence := "```"
			for strings.Contains(content, fence) {
				fence += "`"
			}
			c.out.WriteString(fence + "\n" + content + "\n" + fence)
		} else {
			c.out.WriteString(content)
		}
		c.block()
	case atom.Code, atom.Kbd, atom.Samp:
		if !c.markdown() {
			return c.children(node, state)
		}
		content := textOf(node)
		fence := "`"
		if strings.Contains(content, "`") {
			fence = "``"
		}
		c.flushSpace()
		c.out.WriteString(fence + content + fence)
	case atom.Strong, atom.B:
		return c.inlineMark(node, state, "**")
	case atom.Em, atom.I:
		return c.inlineMark(node, state, "*")
	case atom.A:
		href := resolveLink(c.page, attr(node, "href"))
		text := strings.Join(strings.Fields(textOf(node)), " ")
		if text == "" {
			return nil
		}
		c.flushSpace()
		if c.markdown() && href != "" {
			c.out.WriteString("[" + text + "](" + href + ")")
		} else {
			c.out.WriteString(text)
		}
		c.pendingSpace = true
	case atom.Img:
		alt := strings.Join(strings.Fields(attr(node, "alt")), " ")
		if alt != "" {
			c.flushSpace()
			c.out.WriteString("[image: " + alt + "]")
			c.pendingSpace = true
		}
	case atom.Ul, atom.Ol:
		return c.list(node, state)
	case atom.Li:
		c.newline()
		c.out.WriteString(strings.Repeat("  ", state.listDepth) + "- ")
		return c.children(node, state)
	case atom.Blockquote:
		// Render the quote separately, then prefix each line so nested blocks
		// keep their own layout inside the quote.
		inner := c.child()
		if err := inner.children(node, walkState{}); err != nil {
			return err
		}
		quoted := strings.TrimSpace(collapseBlankLines(inner.out.String()))
		if quoted == "" {
			return nil
		}
		c.block()
		prefix := "> "
		if !c.markdown() {
			prefix = "  "
		}
		lines := strings.Split(quoted, "\n")
		for index, line := range lines {
			if index > 0 {
				c.out.WriteString("\n")
			}
			c.out.WriteString(strings.TrimRight(prefix+line, " "))
		}
		c.block()
	case atom.Table:
		return c.table(node)
	default:
		return c.children(node, state)
	}
	return nil
}

func (c *converter) list(node *html.Node, state walkState) error {
	if state.listDepth == 0 {
		c.block()
	} else {
		c.newline()
	}
	next := walkState{listDepth: state.listDepth + 1}
	ordered := node.DataAtom == atom.Ol
	number := 0
	if start := attr(node, "start"); start != "" {
		if value, err := strconv.Atoi(start); err == nil && value > 1 {
			number = value - 1
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.DataAtom != atom.Li {
			if err := c.walk(child, next); err != nil {
				return err
			}
			continue
		}
		number++
		c.newline()
		c.out.WriteString(strings.Repeat("  ", state.listDepth))
		if ordered {
			c.out.WriteString(strconv.Itoa(number) + ". ")
		} else {
			c.out.WriteString("- ")
		}
		if err := c.children(child, next); err != nil {
			return err
		}
	}
	if state.listDepth == 0 {
		c.block()
	} else {
		c.newline()
	}
	return nil
}

func (c *converter) children(node *html.Node, state walkState) error {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := c.walk(child, state); err != nil {
			return err
		}
	}
	return nil
}

func (c *converter) inlineMark(node *html.Node, state walkState, mark string) error {
	if !c.markdown() || strings.TrimSpace(textOf(node)) == "" {
		return c.children(node, state)
	}
	c.flushSpace()
	c.out.WriteString(mark)
	if err := c.children(node, state); err != nil {
		return err
	}
	c.pendingSpace = false
	c.out.WriteString(mark)
	c.pendingSpace = true
	return nil
}

func (c *converter) table(node *html.Node) error {
	var rows [][]string
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			switch child.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				collect(child)
			case atom.Tr:
				var cells []string
				for cell := child.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
						text := strings.Join(strings.Fields(textOf(cell)), " ")
						cells = append(cells, strings.ReplaceAll(text, "|", `\|`))
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			}
		}
	}
	collect(node)
	if len(rows) == 0 {
		return nil
	}
	c.block()
	for index, cells := range rows {
		if index > 0 {
			c.out.WriteString("\n")
		}
		if c.markdown() {
			c.out.WriteString("| " + strings.Join(cells, " | ") + " |")
			// Markdown tables need a separator after the first row; a table
			// without <th> cells still gets one so the rows stay tabular.
			if index == 0 {
				c.out.WriteString("\n|" + strings.Repeat(" --- |", len(cells)))
			}
		} else {
			c.out.WriteString(strings.Join(cells, " | "))
		}
	}
	c.block()
	return nil
}

func (c *converter) writeText(text string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		if text != "" {
			c.pendingSpace = true
		}
		return
	}
	if isSpace(text[0]) {
		c.pendingSpace = true
	}
	c.flushSpace()
	c.out.WriteString(strings.Join(fields, " "))
	c.pendingSpace = isSpace(text[len(text)-1])
}

func isSpace(b byte) bool { return b == ' ' || b == '\n' || b == '\t' || b == '\r' }

func (c *converter) flushSpace() {
	if c.pendingSpace {
		if body := c.out.String(); body != "" && !strings.HasSuffix(body, "\n") && !strings.HasSuffix(body, " ") {
			c.out.WriteString(" ")
		}
		c.pendingSpace = false
	}
}

// block separates block-level content with one blank line.
func (c *converter) block() {
	c.pendingSpace = false
	body := c.out.String()
	if body == "" || strings.HasSuffix(body, "\n\n") {
		return
	}
	if strings.HasSuffix(body, "\n") {
		c.out.WriteString("\n")
		return
	}
	c.out.WriteString("\n\n")
}

func (c *converter) newline() {
	c.pendingSpace = false
	body := c.out.String()
	if body != "" && !strings.HasSuffix(body, "\n") {
		c.out.WriteString("\n")
	}
}

func attr(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute.Val
		}
	}
	return ""
}

func hasAttr(node *html.Node, name string) bool {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return true
		}
	}
	return false
}

var blankLines = regexp.MustCompile(`\n[ \t]*\n([ \t]*\n)+`)

func collapseBlankLines(text string) string {
	text = normalizeNewlines(text)
	return blankLines.ReplaceAllString(text, "\n\n")
}

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}
