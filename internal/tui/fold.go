package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

type foldKind uint8

const (
	foldNone foldKind = iota
	foldProcess
	foldCalls
	foldThinking
	foldTool
)

// Entry indices remain stable until the visible branch is reset. Call groups
// use their first entry, so arriving siblings never change the group's identity.
type foldTarget struct {
	kind foldKind
	id   int
}

func (m model) foldExpanded(target foldTarget) bool {
	if target.kind == foldProcess {
		for _, group := range m.processGroups {
			if group.id == target.id {
				return !group.collapsed
			}
		}
		return false
	}
	if expanded, ok := m.folds[target]; ok {
		return expanded
	}
	return target.kind == foldCalls
}

func (m *model) setFoldExpanded(target foldTarget, expanded bool) {
	if target.kind == foldNone {
		return
	}
	if target.kind == foldProcess {
		if group := m.processGroup(target.id); group != nil {
			group.collapsed, group.manual = !expanded, true
		}
		return
	}
	if m.folds == nil {
		m.folds = make(map[foldTarget]bool)
	}
	m.folds[target] = expanded
	// Interacting with a child also keeps the parent open during streaming.
	if target.id >= 0 && target.id < len(m.entries) {
		if group := m.processGroup(m.entries[target.id].processID); group != nil {
			group.manual = true
		}
	}
}

// Ctrl+O remains the keyboard shortcut to expand/collapse all main details.
// Individual parent clicks only toggle that parent and retain child choices.
func (m *model) expandAllDetails(expanded bool) {
	for index, entry := range m.entries {
		if entry.kind == entryTool {
			m.setFoldExpanded(foldTarget{kind: foldTool, id: index}, expanded)
			m.setFoldExpanded(foldTarget{kind: foldCalls, id: index}, expanded)
		}
		if entry.thinking != "" {
			m.setFoldExpanded(foldTarget{kind: foldThinking, id: index}, expanded)
		}
	}
}

func foldHeading(label string, expanded bool, indent int) string {
	return foldHeadingStyled(label, expanded, indent, mutedStyle)
}

func foldHeadingStyled(label string, expanded bool, indent int, style lipgloss.Style) string {
	arrow := "▸"
	if expanded {
		arrow = "▾"
	}
	return strings.Repeat(" ", indent) + style.Render(arrow) + " " + label
}

func (m model) processContentItems(start, end int) []transcriptItem {
	var items []transcriptItem
	for index := start; index < end; {
		entry := m.entries[index]
		if entry.kind == entryTool {
			last := index + 1
			for last < end && m.entries[last].kind == entryTool {
				last++
			}
			target := foldTarget{kind: foldCalls, id: index}
			heading := foldHeading(toolGroupSummary(m.entries[index:last]), m.foldExpanded(target), 2)
			item := staticTranscriptItem(index*16+8, heading)
			item.fold, item.gap = target, 1
			items = append(items, item)
			if m.foldExpanded(target) {
				for i := index; i < last; i++ {
					items = append(items, m.foldedToolItems(i)...)
				}
			}
			index = last
			continue
		}
		live := m.running && index == m.assistantEntry && !entry.complete
		if entry.kind == entryAssistant {
			if strings.TrimSpace(entry.thinking) != "" {
				target := foldTarget{kind: foldThinking, id: index}
				label := "Thinking"
				if live {
					label += " · " + m.activityIndicator()
				}
				item := staticTranscriptItem(index*16+5, foldHeading(mutedStyle.Render(label), m.foldExpanded(target), 2))
				item.fold, item.gap = target, 1
				items = append(items, item)
				if m.foldExpanded(target) {
					items = append(items, m.transcriptEntryItem(index, entry, live, transcriptThinking))
				}
			}
			if !entry.conclusion && assistantHasContent(entry, live && strings.TrimSpace(entry.thinking) == "", false, true) {
				item := m.transcriptEntryItem(index, entry, live, transcriptConclusion)
				item.gap = 1
				items = append(items, item)
			}
		} else {
			item := m.transcriptEntryItem(index, entry, live, transcriptProcess)
			item.gap = 1
			items = append(items, item)
		}
		index++
	}
	if len(items) > 0 {
		items[0].gap = 0
	}
	return items
}

func (m model) foldedToolItems(index int) []transcriptItem {
	entry := m.entries[index]
	target := foldTarget{kind: foldTool, id: index}
	entry.toolExpanded = m.foldExpanded(target)
	heading := foldHeading(m.toolHeaderView(entry), entry.toolExpanded, 4)
	header := staticTranscriptItem(index*16+9, heading)
	header.fold = target
	header.hoverText = foldHeadingStyled(m.toolHeaderStyled(entry, true), entry.toolExpanded, 4, transcriptHoverStyle)
	items := []transcriptItem{header}
	if entry.toolExpanded {
		items = append(items, transcriptItem{key: index*16 + 10, version: entry, render: func() string {
			return lipgloss.NewStyle().PaddingLeft(6).Render(m.toolBodyView(entry))
		}})
	}
	return items
}

func toolGroupSummary(entries []transcriptEntry) string {
	counts := make(map[string]int)
	var order []string
	for _, entry := range entries {
		label := "tool"
		switch entry.toolName {
		case "read":
			label = "file read"
		case "skill":
			label = "skill"
		case "bash":
			label = "command"
		case "edit":
			label = "edit"
		case "write":
			label = "write"
		}
		if counts[label] == 0 {
			order = append(order, label)
		}
		counts[label]++
	}
	parts := make([]string, 0, len(order))
	for _, label := range order {
		plural := ""
		if counts[label] != 1 {
			plural = "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s%s", counts[label], label, plural))
	}
	return mutedStyle.Render(strings.Join(parts, " · "))
}
