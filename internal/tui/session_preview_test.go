package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSessionPreviewMarkdown(t *testing.T) {
	t.Parallel()
	prose := "# Heading\n\n**Strong** and [reference](https://example.com)\n\n- [x] done\n- [ ] pending"
	for _, width := range []int{24, 75} {
		var preview sessionPreviewLayout
		text := "Last activity · 2026-09-18 12:34\n\n" + prose
		view := preview.render(text, width)
		shared, err := renderMarkdownBlocks(prose, width)
		if err != nil {
			t.Fatal(err)
		}
		if view != ansi.Hardwrap(shared.view, width, true) {
			t.Fatal("preview diverged from transcript Markdown styling")
		}
		plain := ansi.Strip(view)
		for _, want := range []string{"Heading", "Strong", "✅ done", "⏳ pending"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("missing %q in preview: %q", want, plain)
			}
		}
		if strings.Contains(plain, "Last activity") || strings.Contains(plain, "**Strong**") {
			t.Fatal("preview rendered metadata or retained raw Markdown")
		}
		code := "\n\n```go\n// **literal**\nfunc main() {}\n```"
		view = preview.render(text+code, width)
		plain = ansi.Strip(view)
		if !strings.Contains(plain, "**literal**") || !strings.Contains(plain, "func main() {}") || strings.Contains(plain, "[Copy]") {
			t.Fatalf("preview code lost its literal text or exposed a dead copy control: %q", plain)
		}
		for _, row := range strings.Split(view, "\n") {
			if ansi.StringWidth(row) > width {
				t.Fatalf("preview overflow: %q", row)
			}
		}
		if resized := preview.render(text+code, width+10); resized == view {
			t.Fatal("resizing reused the old preview layout")
		}
	}
}

func TestSessionPreviewStatusAndUnsafeText(t *testing.T) {
	t.Parallel()
	var preview sessionPreviewLayout
	status := "*missing* session\nTry another session."
	if view := preview.render(status, 75); view != status {
		t.Fatalf("status was interpreted as Markdown: %q", view)
	}
	text := "Last activity · date\n\n# Safe\n\n\x1b]52;c;payload\a\n\n```text\n\x1b]52;c;payload\a\n```"
	if view := preview.render(text, 75); strings.Contains(view, "\x1b]52;") || !strings.Contains(ansi.Strip(view), "Safe") {
		t.Fatal("preview lost content or emitted a terminal command")
	}
}
