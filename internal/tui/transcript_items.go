package tui

import "strings"

type transcriptEntryMode uint8

const (
	transcriptStandalone transcriptEntryMode = iota
	transcriptProcess
	transcriptThinking
	transcriptConclusion
)

// Build cheap block descriptions; formatting and wrapping belong to the
// viewport. One process can contain hundreds of reasoning blocks, so a process
// is deliberately not a single rendering item.
func (m model) transcriptItems() []transcriptItem {
	if m.side.isVisible {
		return m.sideTranscriptItems()
	}
	items := make([]transcriptItem, 0, len(m.entries)+2)
	add := func(item transcriptItem, gap int) {
		if len(items) > 0 {
			item.gap = gap
		}
		items = append(items, item)
	}
	groups := make(map[int]processGroup, len(m.processGroups))
	for _, g := range m.processGroups {
		groups[g.id] = g
	}
	for index := 0; index < len(m.entries); {
		e := m.entries[index]
		active := m.running && index == m.assistantEntry && !e.complete
		if e.processID == 0 {
			if e.kind != entryAssistant || assistantHasContent(e, active, true, true) {
				add(m.transcriptEntryItem(index, e, active, transcriptStandalone), 1)
			}
			index++
			continue
		}
		start, end := index, index+1
		for end < len(m.entries) && m.entries[end].processID == e.processID {
			end++
		}
		g := groups[e.processID]
		hasProcess := false
		conclusion := -1
		for i := start; i < end; i++ {
			entry := m.entries[i]
			live := m.running && i == m.assistantEntry && !entry.complete
			if entry.kind == entryAssistant && entry.conclusion {
				conclusion = i
				hasProcess = hasProcess || strings.TrimSpace(entry.thinking) != ""
			} else {
				hasProcess = hasProcess || entry.kind != entryAssistant || assistantHasContent(entry, live, true, true)
			}
		}
		if hasProcess {
			version := struct {
				Group      processGroup
				Start, End int
			}{g, start, end}
			add(transcriptItem{key: start*16 + 4, version: version, render: func() string { return m.processHeader(start, end, g.collapsed) }}, 1)
			items[len(items)-1].fold = foldTarget{kind: foldProcess, id: g.id}
			if !g.collapsed {
				for _, item := range m.processContentItems(start, end) {
					add(item, item.gap)
				}
			}
		}
		if conclusion >= 0 {
			entry := m.entries[conclusion]
			live := m.running && conclusion == m.assistantEntry && !entry.complete
			if assistantHasContent(entry, live, false, true) {
				if !hasProcess {
					text := m.assistantHeaderView(entry.processID)
					add(staticTranscriptItem(start*16+4, text), 1)
				}
				add(m.transcriptEntryItem(conclusion, entry, live, transcriptConclusion), 1)
			}
		}
		index = end
	}
	if m.authPrompt != nil {
		add(staticTranscriptItem(-1, m.authView()), 1)
	}
	if activity := m.pendingActivityView(); activity != "" {
		add(staticTranscriptItem(-2, activity), 1)
	}
	if steering := m.pendingSteeringView(); steering != "" {
		add(staticTranscriptItem(-3, steering), 1)
	}
	if len(items) == 0 {
		add(staticTranscriptItem(-4, m.welcomeView()), 0)
	}
	return items
}

func assistantHasContent(e transcriptEntry, active, thinking, text bool) bool {
	return thinking && strings.TrimSpace(e.thinking) != "" || text && (strings.TrimSpace(e.text) != "" || active)
}

func staticTranscriptItem(key int, content string) transcriptItem {
	return transcriptItem{key: key, version: content, render: func() string { return content }}
}

// Keys reserve distinct parts for a header and each view of an entry.
func (m *model) transcriptEntryItem(index int, e transcriptEntry, active bool, mode transcriptEntryMode) transcriptItem {
	animation := ""
	if active {
		animation = m.activityIndicator()
	}
	duration := ""
	if mode == transcriptStandalone {
		if d, ok := m.processDuration(e.processID); ok {
			duration = formatRunDuration(d)
		}
	}
	version := struct {
		Entry               transcriptEntry
		Active              bool
		Mode                transcriptEntryMode
		Animation, Duration string
	}{e, active, mode, animation, duration}
	return transcriptItem{key: index*16 + int(mode), version: version, render: func() string {
		if mode == transcriptStandalone || e.kind != entryAssistant {
			return m.entryView(e, active)
		}
		return m.assistantProcessEntryView(e, active, mode != transcriptConclusion, mode != transcriptThinking)
	}}
}

func (m model) sideTranscriptItems() []transcriptItem {
	items := []transcriptItem{staticTranscriptItem(-1, m.sideThreadIntro())}
	if thread := m.side.activeThread(); thread != nil {
		for i, entry := range thread.entries {
			question := transcriptItem{key: i * 2, version: entry.question, render: func() string { return m.sideQuestionView(entry.question) }}
			question.gap = 1
			items = append(items, question)
			active := thread.isRunning && i == thread.assistantEntry
			if !entry.complete && !active && entry.answer == "" && entry.thinking == "" && entry.err == "" {
				continue
			}
			animation := ""
			if active {
				animation = m.spinner.View() + m.side.notice
			}
			version := struct {
				Entry     sideThreadEntry
				Active    bool
				Animation string
			}{entry, active, animation}
			items = append(items, transcriptItem{key: i*2 + 1, version: version, gap: 1, render: func() string { return m.sideAnswerView(entry, active) }})
		}
	}
	return items
}
