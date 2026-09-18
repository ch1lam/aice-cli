package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ch1lam/aice-cli/internal/interaction"
)

type sessionGroupItem struct {
	name      string
	count     int
	collapsed bool
}

func (g sessionGroupItem) FilterValue() string { return g.name }

func (g sessionGroupItem) render(w io.Writer, width int, selected bool) {
	prefix, arrow := "  ", "▾"
	style := infoStyle.Bold(true)
	if selected {
		prefix, style = "› ", labelStyle
	}
	if g.collapsed {
		arrow = "▸"
	}
	available := max(1, width-3)
	heading := ansi.Truncate(fmt.Sprintf("%s%s %s %d", prefix, arrow, g.name, g.count), available, "…")
	line := strings.Repeat("─", max(0, available-ansi.StringWidth(heading)-1))
	_, _ = fmt.Fprint(w, " ", style.Render(heading), " ", mutedStyle.Render(line), "\n")
}

func (m *model) setSessionItems(items []interaction.SessionSummary) {
	p := m.sessionPicker
	selected := selectedSessionKey(p)
	selectedGroup, groupSelected := p.list.SelectedItem().(sessionGroupItem)
	p.results = items
	groups := make(map[string][]interaction.SessionSummary, 3)
	now := time.Now()
	for _, item := range items {
		name := sessionDateGroup(item.UpdatedAt, now)
		groups[name] = append(groups[name], item)
	}
	rows := make([]list.Item, 0, len(items)+len(groups))
	index, firstSession := -1, -1
	for _, name := range []string{"Today", "Yesterday", "Earlier"} {
		members := groups[name]
		if len(members) == 0 {
			continue
		}
		groupIndex := len(rows)
		collapsed := p.collapsedGroups[name]
		rows = append(rows, sessionGroupItem{name: name, count: len(members), collapsed: collapsed})
		if groupSelected && selectedGroup.name == name {
			index = groupIndex
		}
		for _, item := range members {
			if item.Key == selected {
				index = len(rows)
				if collapsed {
					index = groupIndex
				}
			}
			if collapsed {
				continue
			}
			if firstSession < 0 {
				firstSession = len(rows)
			}
			rows = append(rows, sessionListItem{SessionSummary: item, current: item.ID != "" && item.ID == m.sessionID})
		}
	}
	if index < 0 {
		index = max(0, firstSession)
	}
	p.list.SetItems(rows)
	p.list.Select(index)
}

func (m *model) toggleSessionGroup() tea.Cmd {
	p := m.sessionPicker
	group, ok := p.list.SelectedItem().(sessionGroupItem)
	if !ok {
		return nil
	}
	if p.collapsedGroups == nil {
		p.collapsedGroups = make(map[string]bool)
	}
	p.collapsedGroups[group.name] = !group.collapsed
	m.setSessionItems(p.results)
	return m.requestSessionPreview()
}
