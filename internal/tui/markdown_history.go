package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
)

// Completed answers keep a parsed tree, not a fully laid-out transcript. A
// part always contains complete top-level constructs; reference links and
// container ancestry are resolved against the original, unmodified document.
// The presentation and its fold state belong exclusively to the TUI loop.
type historyMarkdown struct {
	markdown string
	document ast.Node
	source   []byte
	marker   string
	parts    []historyMarkdownPart
}

type historyMarkdownPart struct {
	first, end  ast.Node
	start, stop int
	blocks      []codeBlock
}

const historyMarkdownThreshold = 8 * 1024

func parseHistoryMarkdown(markdown string) (*historyMarkdown, error) {
	_, document, source, blocks, marker, err := prepareMarkdown(markdown, nil)
	if err != nil {
		return nil, err
	}
	result := &historyMarkdown{markdown: markdown, document: document, source: source, marker: marker}
	start, blockIndex := 0, 0
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
		result.parts = append(result.parts, historyMarkdownPart{first: first, end: end, start: start, stop: stop, blocks: blocks[blockIndex : blockIndex+count]})
		blockIndex += count
		first, start = end, stop
	}
	return result, nil
}

func (h *historyMarkdown) content(index, width int) transcriptContent {
	width = max(width-assistantBodyStyle.GetHorizontalFrameSize(), 20)
	part := h.parts[index]
	rendered, err := renderMarkdownRange(h.document, part.first, part.end, h.source, width)
	content := transcriptContent{view: rendered}
	if err == nil && len(part.blocks) > 0 {
		content, err = insertMarkdownBlocks(rendered, h.marker, part.blocks, width)
	}
	if err != nil {
		return newCodeBlock(h.markdown[part.start:part.stop], "text").layout(codeBlockOptions{width: width}).content()
	}
	if index == 0 {
		trimmed := strings.TrimLeft(content.view, "\r\n")
		removed := strings.Count(content.view[:len(content.view)-len(trimmed)], "\n")
		for i := range content.blocks {
			content.blocks[i].row -= removed
		}
		content.view = trimmed
	}
	if index == len(h.parts)-1 {
		content.view = strings.TrimRight(content.view, "\r\n")
	} else {
		// The viewport supplies one newline between pieces. Keep every other
		// boundary newline, including the blank row between adjacent paragraphs.
		if end := strings.LastIndexByte(content.view, '\n'); end >= 0 && ansi.Strip(content.view[end+1:]) == "" {
			content.view = content.view[:end]
		}
	}
	return content
}

func (m model) historyAssistantParts(entry transcriptEntry, mode transcriptEntryMode) []transcriptItem {
	p := entry.presentation
	if p == nil {
		p = &assistantPresentation{}
	}
	if p.history == nil || p.history.markdown != entry.text {
		parsed, err := parseHistoryMarkdown(entry.text)
		if err != nil || len(parsed.parts) == 0 {
			return []transcriptItem{{renderContent: func() transcriptContent {
				return m.assistantEntryContent(entry, false, mode != transcriptConclusion, true, mode == transcriptStandalone)
			}}}
		}
		p.history = parsed
		p.markdown = markdownCache{}
		p.textCache = assistantSection{}
	}
	h := p.history
	parts := make([]transcriptItem, len(h.parts))
	for i, part := range h.parts {
		parts[i] = transcriptItem{source: entry.text[part.start:part.stop], renderContent: func() transcriptContent {
			body := h.content(i, m.contentWidth()).pad(assistantBodyStyle.GetPaddingLeft(), assistantBodyStyle.GetPaddingRight())
			if i == 0 {
				var prefix transcriptContent
				if mode != transcriptConclusion {
					prefix.appendText(p.thinkingView(entry.thinking, m.contentWidth(), false))
				}
				prefix.append(body, "\n")
				body = prefix
				if mode == transcriptStandalone {
					heading := transcriptContent{view: assistantBodyStyle.Render(m.assistantHeader(entry.processID))}
					heading.append(body, "\n\n")
					body = heading
				}
			}
			return body.pad(1, 1)
		}}
	}
	return parts
}
