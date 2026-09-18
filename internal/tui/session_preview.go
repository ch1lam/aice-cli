package tui

import "github.com/charmbracelet/x/ansi"

// One bounded preview belongs to the picker. Focus, hover and pending selection
// changes reuse its layout; new text or a resize replaces it.
type sessionPreviewLayout struct {
	text, view string
	width      int
}

func (p *sessionPreviewLayout) render(text string, width int) string {
	if p.text == text && p.width == width {
		return p.view
	}
	header, content := sessionPreviewParts(text)
	view := sanitizeToolDetail(content, true)
	if header != "" {
		options := codeBlockOptions{width: max(width, 6), hideCopy: true}
		if rendered, err := renderMarkdownWithOptions(view, options); err == nil {
			view = rendered.view
		}
	}
	*p = sessionPreviewLayout{text: text, width: width, view: ansi.Hardwrap(view, width, true)}
	return p.view
}
