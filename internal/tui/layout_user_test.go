package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSanitizeMultilineTextKeepsPlainText(t *testing.T) {
	t.Parallel()

	got := sanitizeMultilineText("a\r\nb\rc\td\x1b[31mred\x1b[0m tail\x00\x08")
	// \r\n and \r become \n, tab expands, ANSI stripped,
	// remaining controls become �.
	want := "a\nb\nc    dred tail��"
	if got != want {
		t.Fatalf("multiline = %q, want %q", got, want)
	}
	if strings.Contains(got, "\r") || strings.Contains(got, "\t") || strings.Contains(got, "\x1b") {
		t.Fatalf("multiline left terminal controls: %q", got)
	}
}

func TestUserMessageViewStaysRectangularForLongPaste(t *testing.T) {
	t.Parallel()

	// Representative long CJK paste with ASCII, numbers and symbols, plus
	// hostile controls that must not break the bubble background.
	text := "请调整 AICE 内置 Web 工具的默认授权策略，直接实现代码。\n\n" +
		"本次是默认行为简化，不是关闭 Guard，也不是新增权限框架。\n" +
		"覆盖之前任务书中“web_search / web_fetch 默认每个 Session、\n" +
		"每个来源需要确认”的要求。\n\n" +
		"参考审阅基线：\nmain @ c748cc41edee8708e50f3c74c57a89cedb4060f5\n" +
		"1. web_search 已开启且绑定了用户配置的有效搜索服务：\n   默认自动执行，不弹确认。\n" +
		"2. web_fetch 已开启且目标通过现有检查：\n   默认自动执行，不按调用、域名、项目或 Session 重复确认。\n" +
		strings.Repeat("长文本粘贴测试内容，包含中文和English混合。", 20) + "\n" +
		"ansi\x1b[31mred\x1b[0m tail\r\noverwrite\rmiddle\tcol"

	for _, width := range []int{24, 40, 80, 120} {
		m := newModel(nil, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
		view := m.userMessageView(text)
		if strings.Contains(view, "\r") {
			t.Fatalf("width %d: view contains CR: %q", width, view)
		}
		rows := strings.Split(view, "\n")
		for i, row := range rows {
			if got := ansi.StringWidth(row); got != m.layoutWidth() {
				t.Fatalf("width %d row %d width = %d, want %d: %q", width, i, got, m.layoutWidth(), ansi.Strip(row))
			}
		}
		// The viewport re-wraps with Hardwrap; a rectangular bubble must not split.
		for _, line := range rows {
			if parts := strings.Split(ansi.Hardwrap(line, m.layoutWidth(), true), "\n"); len(parts) != 1 {
				t.Fatalf("width %d: viewport would split bubble row %q", width, ansi.Strip(line))
			}
		}
		stripped := ansi.Strip(view)
		if strings.Contains(view, "\x1b[31m") {
			t.Fatalf("width %d: embedded ANSI leaked: %q", width, stripped)
		}
	}
}
