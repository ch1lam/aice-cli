package tui

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	glamouransi "charm.land/glamour/v2/ansi"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

func layoutMarkdown(markdown string, width int) transcriptContent {
	if strings.TrimSpace(markdown) == "" {
		return transcriptContent{}
	}
	width = max(width-assistantBodyStyle.GetHorizontalFrameSize(), 20)
	result, err := renderMarkdownBlocks(markdown, width)
	if err != nil {
		// A renderer failure must not lose content or leak internal placeholders.
		return newCodeBlock(markdown, "text").layout(codeBlockOptions{width: width}).content()
	}
	return result
}

// Glamour buffers nested blocks internally and has no custom code-block hook.
// Replace only parsed code nodes with a collision-free single-cell marker, let
// Glamour lay out the complete tree (including lists, quotes and references),
// then insert the shared component at those explicit slots. Never locate code
// by matching its content against prose or by guessing indentation.
func renderMarkdownBlocks(markdown string, width int) (transcriptContent, error) {
	return renderMarkdownWithOptions(markdown, codeBlockOptions{width: width})
}

func renderMarkdownWithOptions(markdown string, options codeBlockOptions) (transcriptContent, error) {
	md, document, source, blocks, marker, err := prepareMarkdown(markdown, nil)
	if err != nil {
		return transcriptContent{}, err
	}
	style := inkMarkdownStyle()
	style.CodeBlock = glamouransi.StyleCodeBlock{}
	md.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(
		glamouransi.NewRenderer(glamouransi.Options{Styles: style, WordWrap: options.width}), 1000))))
	var out bytes.Buffer
	if err := md.Renderer().Render(&out, source, document); err != nil {
		return transcriptContent{}, err
	}
	rendered := strings.Trim(out.String(), "\r\n")
	if len(blocks) == 0 {
		return transcriptContent{view: rendered}, nil
	}
	return insertMarkdownBlocks(rendered, marker, blocks, options)
}

func prepareMarkdown(markdown string, context parser.Context) (goldmark.Markdown, ast.Node, []byte, []codeBlock, string, error) {
	source := []byte(markdown)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	if context == nil {
		context = parser.NewContext()
	}
	document := md.Parser().Parse(text.NewReader(source), parser.WithContext(context))
	var nodes []ast.Node
	if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && (node.Kind() == ast.KindFencedCodeBlock || node.Kind() == ast.KindCodeBlock) {
			nodes = append(nodes, node)
		}
		return ast.WalkContinue, nil
	}); err != nil {
		return nil, nil, nil, nil, "", err
	}

	marker := ""
	if len(nodes) > 0 {
		var err error
		marker, err = markdownMarker(markdown)
		if err != nil {
			return nil, nil, nil, nil, "", err
		}
	}
	blocks := make([]codeBlock, 0, len(nodes))
	for _, node := range nodes {
		var code strings.Builder
		for i := 0; i < node.Lines().Len(); i++ {
			line := node.Lines().At(i)
			// Goldmark synthesizes a newline at EOF for parsing; it is not
			// part of the literal code that a later copy action should use.
			line.ForceNewline = false
			code.Write(line.Value(source))
		}
		language := "text"
		if fence, ok := node.(*ast.FencedCodeBlock); ok {
			language = string(fence.Language(source))
		}
		blocks = append(blocks, newCodeBlock(code.String(), language))
		placeholder := ast.NewCodeBlock()
		start := len(source)
		source = append(source, marker...)
		source = append(source, '\n')
		placeholder.Lines().Append(text.NewSegment(start, len(source)))
		node.Parent().ReplaceChild(node.Parent(), node, placeholder)
	}
	return md, document, source, blocks, marker, nil
}

func markdownMarker(source string) (string, error) {
	// Entities can turn into literal characters during Markdown rendering.
	used := make(map[rune]bool)
	for _, r := range html.UnescapeString(source) {
		used[r] = true
	}
	for r := rune(0xE000); r <= 0xF8FF; r++ {
		if !used[r] {
			return string(r), nil
		}
	}
	return "", fmt.Errorf("markdown code marker alphabet exhausted")
}

func insertMarkdownBlocks(rendered, marker string, blocks []codeBlock, options codeBlockOptions) (transcriptContent, error) {
	width := options.width
	var rows []string
	result := transcriptContent{}
	next := 0
	for line := range strings.SplitSeq(rendered, "\n") {
		before, after, found := strings.Cut(line, marker)
		if !found {
			rows = append(rows, line)
			continue
		}
		if next >= len(blocks) || strings.TrimSpace(ansi.Strip(after)) != "" {
			return transcriptContent{}, fmt.Errorf("invalid markdown code slot")
		}
		column := ansi.StringWidth(before)
		// Deep containers can consume the whole terminal. Clip their decorative
		// prefix to leave room for padding and at least one wide grapheme.
		if column > width-6 {
			before = ansi.Truncate(before, max(width-6, 0), "")
			column = ansi.StringWidth(before)
		}
		options.width = max(width-column, 6)
		layout := blocks[next].layout(options)
		result.blocks = append(result.blocks, codeBlockPlacement{layout: layout, row: len(rows), column: column})
		for _, row := range layout.rows {
			rows = append(rows, before+row.text)
		}
		next++
	}
	if next != len(blocks) {
		return transcriptContent{}, fmt.Errorf("missing markdown code slots")
	}
	result.view = strings.Join(rows, "\n")
	return result, nil
}
