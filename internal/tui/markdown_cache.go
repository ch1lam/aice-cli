package tui

import (
	"bufio"
	"bytes"
	"maps"
	"strings"

	glamouransi "charm.land/glamour/v2/ansi"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
)

// This cache belongs to one assistant presentation, on the update loop. Parse
// the whole source on every change: later input can change a list, a setext
// heading, or an earlier reference link. Only the expensive layout is reused.
type markdownCache struct {
	width int
	refs  map[string]markdownReference
	parts []markdownPart
}

type markdownReference struct{ destination, title string }

type markdownPart struct {
	source  string
	content transcriptContent
}

func (c *markdownCache) layout(markdown string, width int) transcriptContent {
	width = max(width-assistantBodyStyle.GetHorizontalFrameSize(), 20)
	content, err := c.render(markdown, width)
	if err != nil {
		*c = markdownCache{}
		return newCodeBlock(markdown, "text").layout(codeBlockOptions{width: width}).content()
	}
	return content
}

func (c *markdownCache) render(markdown string, width int) (transcriptContent, error) {
	context := parser.NewContext()
	_, document, source, blocks, marker, err := prepareMarkdown(markdown, context)
	if err != nil {
		return transcriptContent{}, err
	}
	refs := make(map[string]markdownReference)
	for _, ref := range context.References() {
		refs[string(ref.Label())] = markdownReference{string(ref.Destination()), string(ref.Title())}
	}
	if c.width != width || !maps.Equal(c.refs, refs) {
		c.parts = nil
	}

	var parts []markdownPart
	var out strings.Builder
	result := transcriptContent{}
	row, blockIndex, start := 0, 0, 0
	for first := document.FirstChild(); first != nil; {
		end, stop := first.NextSibling(), len(markdown)
		for end != nil {
			if offset, ok := markdownCheckpoint(end, markdown); ok {
				stop = offset
				break
			}
			end = end.NextSibling()
		}
		count := 0
		for node := first; node != end; node = node.NextSibling() {
			_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
				if entering && n.Kind() == ast.KindCodeBlock {
					count++
				}
				return ast.WalkContinue, nil
			})
		}
		key := markdown[start:stop]
		var part markdownPart
		if i := len(parts); i < len(c.parts) && c.parts[i].source == key {
			part = c.parts[i]
		} else {
			rendered, err := renderMarkdownRange(document, first, end, source, width)
			if err != nil {
				return transcriptContent{}, err
			}
			content := transcriptContent{view: rendered}
			if count > 0 {
				content, err = insertMarkdownBlocks(rendered, marker, blocks[blockIndex:blockIndex+count], codeBlockOptions{width: width})
				if err != nil {
					return transcriptContent{}, err
				}
			}
			// Do not retain an old whole-answer allocation through a small key.
			part = markdownPart{source: strings.Clone(key), content: content}
		}
		parts = append(parts, part)
		out.WriteString(part.content.view)
		for _, block := range part.content.blocks {
			block.row += row
			result.blocks = append(result.blocks, block)
		}
		row += strings.Count(part.content.view, "\n")
		blockIndex += count
		first, start = end, stop
	}
	c.width, c.refs, c.parts = width, refs, parts
	view := out.String()
	trimmed := strings.TrimLeft(view, "\r\n")
	removedRows := strings.Count(view[:len(view)-len(trimmed)], "\n")
	for i := range result.blocks {
		result.blocks[i].row -= removedRows
	}
	result.view = strings.TrimRight(trimmed, "\r\n")
	return result, nil
}

// Split only at a top-level paragraph/heading with a real source position,
// after a node that finishes its output line. Other constructs stay in the
// same group until such a boundary occurs; never split a list or quote inside.
func markdownCheckpoint(node ast.Node, source string) (int, bool) {
	if node.Kind() != ast.KindParagraph && node.Kind() != ast.KindHeading {
		return 0, false
	}
	if node.Lines().Len() == 0 || node.PreviousSibling() == nil {
		return 0, false
	}
	switch node.PreviousSibling().Kind() {
	case ast.KindParagraph, ast.KindHeading, ast.KindList, ast.KindBlockquote, ast.KindCodeBlock:
		start := node.Lines().At(0).Start
		return strings.LastIndexByte(source[:start], '\n') + 1, true
	default:
		return 0, false
	}
}

type markdownRenderFuncs map[ast.NodeKind]renderer.NodeRendererFunc

func (f markdownRenderFuncs) Register(kind ast.NodeKind, render renderer.NodeRendererFunc) {
	f[kind] = render
}

// Use Glamour's public node callbacks with a fresh document render context,
// while keeping the original AST parents/siblings and resolved references.
// Keep boundary newlines: trimming each group would change paragraph spacing.
func renderMarkdownRange(document, first, end ast.Node, source []byte, width int) (string, error) {
	style := inkMarkdownStyle()
	style.CodeBlock = glamouransi.StyleCodeBlock{}
	funcs := make(markdownRenderFuncs)
	glamouransi.NewRenderer(glamouransi.Options{Styles: style, WordWrap: width}).RegisterFuncs(funcs)
	var out bytes.Buffer
	w := bufio.NewWriter(&out)
	if _, err := funcs[ast.KindDocument](w, source, document, true); err != nil {
		return "", err
	}
	for node := first; node != end; node = node.NextSibling() {
		if err := ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if render := funcs[n.Kind()]; render != nil {
				return render(w, source, n, entering)
			}
			return ast.WalkContinue, nil
		}); err != nil {
			return "", err
		}
	}
	if _, err := funcs[ast.KindDocument](w, source, document, false); err != nil {
		return "", err
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}
