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

			rendered := ansi.Strip(renderMarkdown(tt.source, 80))
			if strings.ContainsRune(rendered, '\u00a0') {
				t.Fatalf("rendered %q contains NBSP: %q", tt.source, rendered)
			}
			if !strings.Contains(rendered, tt.want) {
				t.Errorf("rendered %q = %q, want substring %q", tt.source, rendered, tt.want)
			}
		})
	}
}
