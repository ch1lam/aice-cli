package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdownPreservesCJKCodeSpanSpacing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "cjk without code keeps adjacency",
			source: "这是glamour全系主题",
			want:   "这是glamour全系主题",
		},
		{
			name:   "code without surrounding spaces adds none",
			source: "这是`glamour`官方渲染",
			want:   "这是glamour官方渲染",
		},
		{
			name:   "code with surrounding spaces keeps single spaces",
			source: "这是 `glamour` 官方渲染",
			want:   "这是 glamour 官方渲染",
		},
		{
			name:   "code with symbols keeps adjacency",
			source: "修复`slice bounds out of range [1:0]`问题",
			want:   "修复slice bounds out of range [1:0]问题",
		},
		{
			name:   "list item code keeps single spaces",
			source: "- `charm.land/glamour/v2@v2.0.1` 默认 `Code.Prefix`",
			want:   "• charm.land/glamour/v2@v2.0.1 默认 Code.Prefix",
		},
		{
			name:   "plain cjk english spacing is preserved as-is",
			source: "注释写着防止 hard breaks，这里没有用行内代码块",
			want:   "注释写着防止 hard breaks，这里没有用行内代码块",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rendered := ansi.Strip(layoutMarkdown(tt.source, 80).view)
			if strings.ContainsRune(rendered, '\u00a0') {
				t.Fatalf("rendered %q contains NBSP: %q", tt.source, rendered)
			}
			if !strings.Contains(rendered, tt.want) {
				t.Errorf("rendered %q = %q, want substring %q", tt.source, rendered, tt.want)
			}
		})
	}
}

func TestSlashMenuHighlightsMatchesWithoutBoldingDescriptions(t *testing.T) {
	t.Parallel()
	view := renderSlashMenuRows(80, "Choices", "", []slashMenuRow{
		{label: "Low", description: "SELECTED_DESCRIPTION", query: "lw", current: true},
		{label: "Lower", description: "OTHER_DESCRIPTION", query: "lw"},
	}, 0)
	selectedStyle := slashCommandSelectedStyle
	if !strings.Contains(view, selectedStyle.Render("› ")) {
		t.Fatal("selected arrow does not use the bold option style")
	}
	if strings.Contains(view, ";48;") || strings.Contains(view, "\x1b[48;") {
		t.Fatal("slash menu introduced a selection background")
	}
	for _, matched := range []string{"L", "w"} {
		if !strings.Contains(view, selectedStyle.Foreground(secondaryColor).Render(matched)) {
			t.Fatalf("selected match %q is not tinted and bold", matched)
		}
	}
	if !strings.Contains(view, mutedStyle.Render("SELECTED_DESCRIPTION")) {
		t.Fatal("selected description is not muted at normal weight")
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(line), "Lower") && strings.Contains(line, "\x1b[1;") {
			t.Fatal("unselected fuzzy match became bold")
		}
	}
	if !strings.Contains(ansi.Strip(view), "Low (active)") {
		t.Fatal("selected current value lost its independent active marker")
	}
}
