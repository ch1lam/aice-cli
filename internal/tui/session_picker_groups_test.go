package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

func groupedPickerModel(t *testing.T) model {
	t.Helper()
	m := pickerModel(t, 100, 28)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	items := []interaction.SessionSummary{
		{Key: "today-1", Title: "Today needle", UpdatedAt: today.UnixMilli()},
		{Key: "today-2", Title: "Today second", UpdatedAt: today.UnixMilli()},
		{Key: "yesterday", Title: "Yesterday task", UpdatedAt: today.AddDate(0, 0, -1).UnixMilli()},
		{Key: "earlier", Title: "Earlier task", UpdatedAt: today.AddDate(0, 0, -2).UnixMilli()},
	}
	m.sessionPicker.all = items
	m.setSessionItems(items)
	return m
}

func TestSessionGroupNavigationAndToggle(t *testing.T) {
	t.Parallel()
	m := groupedPickerModel(t)
	if selectedSessionKey(m.sessionPicker) != "today-1" {
		t.Fatal("picker did not select first session")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	group := m.sessionPicker.list.SelectedItem().(sessionGroupItem)
	if group.name != "Today" || !group.collapsed || group.count != 2 || len(m.sessionPicker.list.Items()) != 5 || m.running {
		t.Fatal("Enter did not collapse only the selected group")
	}
	if !strings.Contains(ansi.Strip(m.sessionPickerView()), "▸ Today 2") ||
		!strings.Contains(ansi.Strip(m.sessionPickerView()), "4 sessions") {
		t.Fatal("collapsed heading or session total is wrong")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if group := m.sessionPicker.list.SelectedItem().(sessionGroupItem); group.name != "Yesterday" {
		t.Fatal("navigation did not skip folded sessions")
	}
	m = updateModel(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.sessionPicker.list.Items()) != 7 || m.sessionPicker.list.SelectedItem().(sessionGroupItem).collapsed {
		t.Fatal("Enter did not expand the wheel-selected group")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if selectedSessionKey(m.sessionPicker) != "today-1" {
		t.Fatal("expanded session is unreachable")
	}
	l := m.sessionPickerLayout()
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 5, Y: l.y + 3, Button: tea.MouseLeft})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.sessionPicker.list.SelectedItem().(sessionGroupItem).collapsed {
		t.Fatal("mouse did not select the painted group heading")
	}
}

func TestSessionGroupRefreshAndSearch(t *testing.T) {
	t.Parallel()
	m := groupedPickerModel(t)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	items := append([]interaction.SessionSummary(nil), m.sessionPicker.results...)
	items = append(items, interaction.SessionSummary{Key: "new", Title: "New arrival", UpdatedAt: items[0].UpdatedAt})
	m = updateModel(t, m, sessionSearchResult{generation: m.sessionQueryGeneration, items: items})
	group := m.sessionPicker.list.SelectedItem().(sessionGroupItem)
	if group.name != "Today" || !group.collapsed || group.count != 3 || len(m.sessionPicker.list.Items()) != 5 {
		t.Fatal("catalog refresh lost folds, count or selection")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updateModel(t, m, tea.PasteMsg{Content: "needle"})
	if len(m.sessionPicker.list.Items()) != 2 || m.sessionPicker.list.SelectedItem().(sessionGroupItem).collapsed {
		t.Fatal("new search did not reveal a matching session in a folded group")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if selectedSessionKey(m.sessionPicker) != "today-1" {
		t.Fatal("filtered session is unreachable")
	}
}

func TestSessionGroupDoesNotLoadOrRestoreSession(t *testing.T) {
	t.Parallel()
	m := groupedPickerModel(t)
	cancelled := false
	m.previewSession = func(generation uint64, key, query string) (tea.Cmd, context.CancelFunc) {
		return func() tea.Msg { return sessionPreviewResult{generation: generation, text: "obsolete session"} },
			func() { cancelled = true }
	}
	next, pending := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(model)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	next, command := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(model)
	if command != nil || !cancelled || !strings.Contains(m.sessionPicker.previewText, "Today · 2 sessions") || m.sessionPicker.notice != "" {
		t.Fatal("group selection loaded a session or retained pending preview state")
	}
	m = updateModel(t, m, pending())
	if strings.Contains(m.sessionPicker.previewText, "obsolete") {
		t.Fatal("obsolete session preview replaced group instructions")
	}
	for _, key := range []rune{tea.KeyF2, tea.KeyF4, tea.KeyEnter} {
		next, command = m.Update(tea.KeyPressMsg{Code: key})
		m = next.(model)
		if command != nil || m.running || m.sessionPicker.rename != nil || m.reading != nil {
			t.Fatal("group dispatched a session operation")
		}
	}
}

func TestSessionListTextLeavesTimeColumnClear(t *testing.T) {
	t.Parallel()
	for _, width := range []int{24, 60, 160} {
		m := pickerModel(t, width, 24)
		m.setSessionItems([]interaction.SessionSummary{{Key: "long", Title: strings.Repeat("标题", 100),
			Snippet: strings.Repeat("描述", 100), UpdatedAt: time.Now().Add(-10 * time.Minute).UnixMilli()}})
		var rendered strings.Builder
		sessionItemDelegate{}.Render(&rendered, m.sessionPicker.list, m.sessionPicker.list.Index(), m.sessionPicker.list.SelectedItem())
		rows := strings.Split(ansi.Strip(rendered.String()), "\n")
		paneWidth := m.sessionPickerLayout().listWidth
		for _, row := range rows {
			if lipgloss.Width(row) > paneWidth {
				t.Fatal("long text overflowed the list")
			}
		}
		if paneWidth >= 16 && (!strings.HasSuffix(rows[0], "   10min  ") || lipgloss.Width(rows[1]) > paneWidth-10) {
			t.Fatal("title or snippet consumed the time-column gap or right padding")
		}
	}
}

func TestSessionGroupPaginationAndMouseRows(t *testing.T) {
	t.Parallel()
	m := groupedPickerModel(t)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 16})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if group, ok := m.sessionPicker.list.SelectedItem().(sessionGroupItem); !ok || group.name != "Earlier" {
		t.Fatal("page navigation did not reach the next page's group heading")
	}
	l := m.sessionPickerLayout()
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 5, Y: l.y + 3, Button: tea.MouseLeft})
	if selectedSessionKey(m.sessionPicker) != "yesterday" {
		t.Fatal("mouse selected the wrong session on the second page")
	}
	m = updateModel(t, m, tea.MouseClickMsg{X: l.x + 5, Y: l.y + 5, Button: tea.MouseLeft})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if group, ok := m.sessionPicker.list.SelectedItem().(sessionGroupItem); !ok || group.name != "Earlier" || !group.collapsed {
		t.Fatal("mouse did not select the painted heading on the second page")
	}
}
