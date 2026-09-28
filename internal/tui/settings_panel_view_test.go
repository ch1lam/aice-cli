package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

func settingsPaintedMouse(t *testing.T, m model, text string) tea.Mouse {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.settingsPanelView()), "\n") {
		if x := strings.Index(line, text); x >= 0 {
			return tea.Mouse{
				X: m.settings.layout.x + ansi.StringWidth(line[:x]),
				Y: m.settings.layout.y + y, Button: tea.MouseLeft,
			}
		}
	}
	t.Fatalf("missing %q in settings", text)
	return tea.Mouse{}
}

func TestSettingsPaddingTabsAndSearchGeometry(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {32, 16}, {24, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := panelModel(t, size[0], size[1])
			view := m.settingsPanelView()
			l := m.settings.layout
			if lipgloss.Width(view) != l.width || lipgloss.Height(view) != l.height {
				t.Fatalf("frame dimensions = %dx%d, want %dx%d", lipgloss.Width(view), lipgloss.Height(view), l.width, l.height)
			}
			lines := strings.Split(ansi.Strip(view), "\n")
			for y, line := range lines[1 : len(lines)-1] {
				if ansi.Cut(line, 1, 2) != " " || ansi.Cut(line, l.width-2, l.width-1) != " " {
					t.Fatalf("missing horizontal padding at row %d: %q", y+1, line)
				}
			}
			for _, y := range []int{1, 3, 5, len(lines) - 3} {
				if strings.TrimSpace(ansi.Cut(lines[y], 1, l.width-1)) != "" {
					t.Fatalf("missing vertical padding: %q", lines[y])
				}
			}
			for _, y := range []int{2, 4} {
				content := ansi.Cut(lines[y], 2, l.width-2)
				if !strings.HasPrefix(content, "  ") || strings.HasPrefix(content, "   ") {
					t.Fatalf("tab or search is not aligned: %q", lines[y])
				}
			}
			footer := ansi.Cut(lines[len(lines)-2], 2, l.width-2)
			left := len(footer) - len(strings.TrimLeft(footer, " "))
			right := len(footer) - len(strings.TrimRight(footer, " "))
			if !strings.Contains(footer, "Tab category") || right-left < 0 || right-left > 1 {
				t.Fatalf("footer is not centered against the bottom border: %q", footer)
			}
			if strings.Contains(lines[2], "[") || strings.Contains(lines[2], "]") {
				t.Fatal("tab still has brackets")
			}
			if size[0] >= 120 {
				mouse := settingsPaintedMouse(t, m, "Run Limits")
				m = updateModel(t, m, tea.MouseClickMsg(mouse))
				m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
				if m.settings.tab != 2 {
					t.Fatal("click on painted tab selected another category")
				}
			}
			mouse := settingsPaintedMouse(t, m, "/ Search")
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			m = updateModel(t, m, tea.PasteMsg{Content: "中文"})
			cursor := m.View().Cursor
			text := settingsPaintedMouse(t, m, "中文")
			if cursor == nil || cursor.X != text.X+4 || cursor.Y != text.Y {
				t.Fatalf("search cursor = %+v, text at %+v", cursor, text)
			}
		})
	}
}

func TestSettingsSelectedExplanationAtBottom(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {32, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := panelModel(t, size[0], size[1])
			p := m.settings
			p.snapshot.Fields = []interaction.SettingField{
				{ID: "model", Category: "models", Label: "Model with a label extending beyond the old sidebar boundary",
					Description: "Choose the next model. Further details only.", Kind: interaction.SettingString},
				{ID: "thinking", Category: "models", Label: "Thinking", Kind: interaction.SettingString,
					Description: strings.Repeat("推理级别决定模型回答之前的思考深度", 8)},
			}
			l := p.layout
			view := ansi.Strip(m.settingsPanelView())
			lines := strings.Split(view, "\n")
			if size[0] == 120 && !strings.Contains(view, p.snapshot.Fields[0].Label) {
				t.Fatal("list still truncates labels at the old sidebar boundary")
			}
			if strings.Contains(view, "Further details") || strings.Contains(view, "Save to:") {
				t.Fatal("full details leaked into the summary")
			}
			summary := strings.TrimSpace(ansi.Cut(lines[len(lines)-4], 2, l.width-2))
			if summary != "Choose the next model." {
				t.Fatalf("summary above footer gap = %q", summary)
			}
			mouse := settingsPaintedMouse(t, m, "Choose")
			if got := p.target(mouse); got != "" {
				t.Fatalf("summary is clickable: %q", got)
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			lines = strings.Split(ansi.Strip(m.settingsPanelView()), "\n")
			for _, y := range []int{len(lines) - 5, len(lines) - 4} {
				if !strings.Contains(lines[y], "思") && !strings.Contains(lines[y], "推") {
					t.Fatalf("missing wrapped explanation at row %d: %q", y, lines[y])
				}
			}
			if !strings.Contains(lines[len(lines)-4], "…") {
				t.Fatal("long explanation was not limited to two lines")
			}
			if strings.TrimSpace(ansi.Cut(lines[len(lines)-6], 2, l.width-2)) != "" {
				t.Fatal("missing gap between list and explanation")
			}
			m = updateModel(t, m, tea.KeyPressMsg{Code: '?', Text: "?"})
			if m.settings.editing == nil || !strings.Contains(m.settings.editing.Description, "Save to:") {
				t.Fatal("full details are no longer accessible")
			}
		})
	}
}

func TestSettingsGroupedRowsStaySelectableAfterScrolling(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {32, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := panelModel(t, size[0], size[1])
			m.settings.tab = 1
			m.settings.snapshot.Fields = nil
			// Interleaved source fields must become contiguous groups without
			// losing IDs or making section headings keyboard-selectable.
			for i := range 12 {
				for _, prefix := range []string{"browser.", "desktop.", "web.search.", "web.fetch."} {
					id := fmt.Sprintf("%s%02d", prefix, i)
					m.settings.snapshot.Fields = append(m.settings.snapshot.Fields, interaction.SettingField{
						ID: id, Category: "tools", Label: id, Kind: interaction.SettingString,
						Value: interaction.SettingValue{Kind: interaction.SettingString, Text: "v"},
					})
				}
			}
			fields := m.settings.fields()
			for i, field := range fields {
				if i > 0 {
					m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
				}
				mouse := settingsPaintedMouse(t, m, "› "+field.Label)
				if got := m.settings.target(mouse); got != "field:"+field.ID {
					t.Fatalf("painted field %q has target %q", field.ID, got)
				}
				for y, line := range strings.Split(ansi.Strip(m.settingsPanelView()), "\n") {
					if strings.Contains(line, "──") {
						mouse := tea.Mouse{X: m.settings.layout.x + 4, Y: m.settings.layout.y + y}
						if strings.HasPrefix(m.settings.target(mouse), "field:") {
							t.Fatalf("section/divider is clickable as a field: %q", line)
						}
					}
				}
			}
			last := fields[len(fields)-1]
			mouse := settingsPaintedMouse(t, m, "› "+last.Label)
			m = updateModel(t, m, tea.MouseClickMsg(mouse))
			m = updateModel(t, m, tea.MouseReleaseMsg(mouse))
			if m.settings.editing == nil || m.settings.editing.ID != last.ID {
				t.Fatal("scrolled field click edited a different setting")
			}
		})
	}
}
