package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
)

// Completed answers keep a parsed tree, not a fully laid-out transcript. A
// part contains complete top-level constructs or complete list items. Links
// and container ancestry use the original, unmodified document.
// The presentation and its fold state belong exclusively to the TUI loop.
type historyMarkdown struct {
	markdown    string
	document    ast.Node
	source      []byte
	marker      string
	tableMarker string
	parts       []historyMarkdownPart
}

type historyMarkdownPart struct {
	first, end  ast.Node
	container   ast.Node
	start, stop int
	blocks      []codeBlock
	tables      []tableBlock
}

const (
	historyMarkdownThreshold = 8 * 1024
	historyCodeLineLimit     = 80
	historyListChunkItems    = 16
)

func parseHistoryMarkdown(markdown string) (*historyMarkdown, error) {
	_, document, source, blocks, marker, err := prepareMarkdown(markdown, nil)
	if err != nil {
		return nil, err
	}
	tables, tableMarker, source, err := extractMarkdownTables(document, source)
	if err != nil {
		return nil, err
	}
	for i := range blocks {
		if len(blocks[i].lines) > historyCodeLineLimit || len(blocks[i].source) >= historyMarkdownThreshold {
			blocks[i].expanded = new(bool)
		}
	}
	result := &historyMarkdown{markdown: markdown, document: document, source: source, marker: marker, tableMarker: tableMarker}
	start, blockIndex, tableIndex := 0, 0, 0
	for first := document.FirstChild(); first != nil; {
		end, stop := first.NextSibling(), len(markdown)
		for end != nil {
			offset, ok := markdownCheckpoint(end, markdown)
			if !ok && end.Kind() == ast.KindList {
				switch end.PreviousSibling().Kind() {
				case ast.KindParagraph, ast.KindHeading:
					offset, ok = historyNodeStart(end, markdown)
				}
			}
			if ok {
				stop = offset
				break
			}
			end = end.NextSibling()
		}
		codeCount, tableCount := countMarkdownPlaceholders(first, end, source, tableMarker)
		result.parts = append(result.parts, historyMarkdownPart{first: first, end: end, start: start, stop: stop, blocks: blocks[blockIndex : blockIndex+codeCount], tables: tables[tableIndex : tableIndex+tableCount]})
		blockIndex += codeCount
		tableIndex += tableCount
		first, start = end, stop
	}
	result.splitLists()
	return result, nil
}

// Split standalone top-level lists at complete items. Complex groups and
// individual nested items retain their full Markdown layout.
func (h *historyMarkdown) splitLists() {
	var parts []historyMarkdownPart
	for _, part := range h.parts {
		if part.first.Kind() != ast.KindList || part.first.NextSibling() != part.end ||
			part.first.ChildCount() <= historyListChunkItems || len(part.blocks) != 0 || len(part.tables) != 0 {
			parts = append(parts, part)
			continue
		}
		list := part.first
		start := part.start
		for first := list.FirstChild(); first != nil; {
			end := first
			for count := 0; count < historyListChunkItems && end != nil; count++ {
				end = end.NextSibling()
			}
			stop := part.stop
			if end != nil {
				// Find the first source line of the next complete item. Empty
				// items have no source segment, so retain the remainder together.
				if offset, ok := historyNodeStart(end, h.markdown); ok {
					stop = offset
				} else {
					end = nil
				}
			}
			parts = append(parts, historyMarkdownPart{first: first, end: end, container: list, start: start, stop: stop})
			first, start = end, stop
		}
	}
	h.parts = parts
}

func historyNodeStart(node ast.Node, source string) (int, bool) {
	for node != nil && node.Type() == ast.TypeBlock {
		if node.Lines().Len() > 0 {
			offset := node.Lines().At(0).Start
			if offset < len(source) {
				return strings.LastIndexByte(source[:offset], '\n') + 1, true
			}
			return 0, false
		}
		node = node.FirstChild()
	}
	return 0, false
}

func (h *historyMarkdown) content(index, width int) transcriptContent {
	width = max(width-assistantBodyStyle.GetHorizontalFrameSize(), 20)
	part := h.parts[index]
	rendered, err := renderMarkdownChildRange(h.document, part.container, part.first, part.end, h.source, width)
	if part.container != nil {
		if part.first != part.container.FirstChild() {
			for {
				line, rest, ok := strings.Cut(rendered, "\n")
				if !ok || strings.TrimSpace(ansi.Strip(line)) != "" {
					break
				}
				rendered = rest
			}
		}
		if part.end != nil {
			for {
				end := strings.LastIndexByte(rendered, '\n')
				if end < 0 || strings.TrimSpace(ansi.Strip(rendered[end+1:])) != "" {
					break
				}
				rendered = rendered[:end]
			}
		}
	}
	content := transcriptContent{view: rendered}
	if err == nil && (len(part.blocks) > 0 || len(part.tables) > 0) {
		content, err = insertMarkdownBlocks(rendered, h.marker, part.blocks, part.tables, h.tableMarker, codeBlockOptions{width: width})
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
		parts[i] = transcriptItem{
			source:      entry.text[part.start:part.stop],
			revealMatch: func(query string) bool { return h.revealMatch(i, query) },
			renderContent: func() transcriptContent {
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
			},
		}
	}
	return parts
}

// Search opens only hidden code that contains the requested text. This is a
// presentation change; the source, branch and Session remain untouched.
func (h *historyMarkdown) revealMatch(part int, query string) bool {
	changed := false
	for _, block := range h.parts[part].blocks {
		if block.expanded != nil && !*block.expanded && strings.Contains(strings.ToLower(block.source), query) {
			*block.expanded, changed = true, true
		}
	}
	return changed
}
