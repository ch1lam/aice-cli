package tui

import (
	"strings"
	"unicode/utf8"
)

// Bound work before terminal wrapping, including reasoning with no newlines.
// This is only a live preview; completed messages retain their full content.
const maximumLiveThinkingBytes = 4096

const liveThinkingNotice = "… earlier thinking hidden while streaming"

// assistantPresentation is owned by the Bubble Tea update loop. Entries hold
// a pointer because strings.Builder must not be copied after its first write.
// Text snapshots remain immutable when the builders receive further deltas.
type assistantPresentation struct {
	textBuffer     strings.Builder
	thinkingBuffer strings.Builder
	textCache      assistantSection
	thinkingCache  assistantSection
	markdown       markdownCache
	history        *historyMarkdown
}

type assistantSection struct {
	source   string
	width    int
	rendered string
	blocks   []codeBlockPlacement
	live     bool
}

func (p *assistantPresentation) appendText(current, delta string) string {
	return appendAssistantText(&p.textBuffer, current, delta)
}

func (p *assistantPresentation) appendThinking(current, delta string) string {
	return appendAssistantText(&p.thinkingBuffer, current, delta)
}

func appendAssistantText(buffer *strings.Builder, current, delta string) string {
	if buffer.Len() == 0 {
		buffer.WriteString(current)
	}
	buffer.WriteString(delta)
	return buffer.String()
}

func (p *assistantPresentation) textContent(source string, width int, live bool) transcriptContent {
	if p != nil && (!live || strings.TrimSpace(source) == "") {
		p.markdown = markdownCache{}
	}
	if p != nil && p.textCache.source == source && p.textCache.width == width && p.textCache.live == live {
		return transcriptContent{view: p.textCache.rendered, blocks: p.textCache.blocks}
	}
	var content transcriptContent
	if strings.TrimSpace(source) != "" {
		if p == nil || !live {
			content = layoutMarkdown(source, width)
		} else {
			content = p.markdown.layout(source, width)
		}
		content = content.pad(assistantBodyStyle.GetPaddingLeft(), assistantBodyStyle.GetPaddingRight())
	}
	if p != nil {
		p.textCache = assistantSection{source: source, width: width, rendered: content.view, blocks: content.blocks, live: live}
	}
	return content
}

func (p *assistantPresentation) thinkingView(source string, width int, live bool) string {
	if live {
		source = liveThinkingTail(source)
	}
	if p != nil && p.thinkingCache.source == source && p.thinkingCache.width == width {
		return p.thinkingCache.rendered
	}
	rendered := ""
	if strings.TrimSpace(source) != "" {
		rendered = thinkingStyle.Width(width).Render(source)
	}
	if p != nil {
		p.thinkingCache = assistantSection{source: source, width: width, rendered: rendered}
	}
	return rendered
}

func liveThinkingTail(source string) string {
	if len(source) <= maximumLiveThinkingBytes {
		return source
	}
	start := len(source) - maximumLiveThinkingBytes
	for start < len(source) && !utf8.RuneStart(source[start]) {
		start++
	}
	return liveThinkingNotice + "\n" + source[start:]
}
