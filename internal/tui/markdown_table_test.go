package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBoxTableRendersOuterBorders(t *testing.T) {
	t.Parallel()

	source := "| Name | Role |\n|------|------|\n| Alice | Eng |\n| Bob | Design |\n"
	rendered := ansi.Strip(layoutMarkdown(source, 60).view)
	for _, corner := range []string{"┌", "┐", "└", "┘", "┬", "┴", "├", "┤", "┼"} {
		if !strings.Contains(rendered, corner) {
			t.Fatalf("box table = %q, missing corner %q", rendered, corner)
		}
	}
	lines := strings.Split(strings.TrimSpace(rendered), "\n")
	if len(lines) != 7 {
		t.Fatalf("box table has %d lines, want 7 (top, header, sep, row, sep, row, bottom):\n%s", len(lines), rendered)
	}
	for _, want := range []string{"Name", "Role", "Alice", "Eng", "Bob", "Design"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("box table = %q, missing cell %q", rendered, want)
		}
	}
}

func TestBoxTableHeaderIsBold(t *testing.T) {
	t.Parallel()

	source := "| Name | Role |\n|------|------|\n| Alice | Eng |\n"
	raw := layoutMarkdown(source, 60).view
	header := ""
	for line := range strings.Lines(raw) {
		if strings.Contains(ansi.Strip(line), "Name") {
			header = line
		}
	}
	if header == "" {
		t.Fatal("header line not found")
	}
	if !strings.Contains(header, "[1m") {
		t.Errorf("header line is not bold: %q", header)
	}
}

func TestBoxTableRespectsAlignment(t *testing.T) {
	t.Parallel()

	source := "| Left | Center | Right |\n|:-----|:------:|------:|\n| a | b | c |\n"
	rendered := ansi.Strip(layoutMarkdown(source, 60).view)
	lines := strings.Split(strings.TrimSpace(rendered), "\n")
	var body string
	for _, line := range lines {
		if strings.Contains(line, "│ a ") {
			body = line
		}
	}
	if body == "" {
		t.Fatalf("body row not found:\n%s", rendered)
	}
	// Right-aligned cell hugs the right border; center cell has padding both sides.
	if !strings.Contains(body, " c │") {
		t.Errorf("right column is not right-aligned: %q", body)
	}
	if !strings.Contains(body, "│ ") || !strings.Contains(body, " │") {
		t.Errorf("center column lost its padding: %q", body)
	}
}

func TestBoxTableKeepsInlineStyles(t *testing.T) {
	t.Parallel()

	source := "| Feature | Desc |\n|---------|------|\n| `code` | use **bold** |\n"
	raw := layoutMarkdown(source, 60).view
	if !strings.Contains(raw, "201;") {
		t.Errorf("inline code lost its gold color: %q", raw)
	}
	if !strings.Contains(raw, "[1m") {
		t.Errorf("bold cell text lost its weight: %q", raw)
	}
	if !strings.Contains(ansi.Strip(raw), "code") || !strings.Contains(ansi.Strip(raw), "bold") {
		t.Errorf("cell text missing: %q", raw)
	}
}

func TestBoxTableWrapsNarrowWidths(t *testing.T) {
	t.Parallel()

	source := "| Name | Notes |\n|------|-------|\n| Alice | likes long walks in the park every morning |\n"
	rendered := ansi.Strip(layoutMarkdown(source, 40).view)
	if !strings.Contains(rendered, "└") || !strings.Contains(rendered, "┘") {
		t.Fatalf("narrow table lost its box:\n%s", rendered)
	}
	for line := range strings.Lines(rendered) {
		if ansi.StringWidth(strings.TrimRight(line, " ")) > 40 {
			t.Errorf("wrapped line exceeds width: %q", line)
		}
	}
}

func TestBoxTableMatchesCacheRender(t *testing.T) {
	t.Parallel()

	source := "before\n\n| a | b |\n|---|---|\n| x | `y` |\n\nafter\n"
	assertMarkdownCacheEqual(t, &markdownCache{}, source, 80)
	assertMarkdownCacheEqual(t, &markdownCache{}, source, 32)
}
