package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ch1lam/aice-cli/internal/interaction"
	"github.com/charmbracelet/x/ansi"
)

type sessionReadingResult struct {
	generation uint64
	view       *interaction.SessionReading
	err        error
}

// A reader has its own presentation model. The previous model retains the
// live draft, catalog, folds and viewport; none of its conversation is replaced.
type sessionReading struct {
	previous      *model
	latest        *interaction.Transcript
	otherBranch   bool
	directory     bool
	turns         []int
	selected      int
	savedViewport transcriptViewport
}

func (m *model) requestSessionReading() tea.Cmd {
	p := m.sessionPicker
	item, ok := p.list.SelectedItem().(sessionListItem)
	if !ok || item.Problem != "" || m.readSession == nil {
		return nil
	}
	if p.cancelPreview != nil {
		p.cancelPreview()
	}
	m.sessionPreviewGeneration++
	p.notice = "Opening history…"
	command, cancel := m.readSession(m.sessionPreviewGeneration, item.Key, item.MatchID)
	p.cancelPreview = cancel
	return command
}

func (m model) applySessionReading(result sessionReadingResult) (tea.Model, tea.Cmd) {
	p := m.sessionPicker
	if p == nil || p.restoring || result.generation != m.sessionPreviewGeneration {
		return m, nil
	}
	if result.err != nil {
		p.notice = result.err.Error()
		return m, nil
	}
	if p.cancelSearch != nil {
		p.cancelSearch()
	}
	p.loading, p.notice = false, ""
	m.sessionQueryGeneration++
	reader := newModel(nil, nil)
	reader.width, reader.height = m.width, m.height
	reader.currentModel, reader.workingDirectory = m.currentModel, m.workingDirectory
	reader.reading = &sessionReading{previous: &m, latest: result.view.Active, otherBranch: result.view.OtherBranch}
	reader.input.Blur()
	reader.resizeLayout()
	reader.replaceTranscript(result.view.Transcript)
	if result.view.FocusID != "" {
		for index, entry := range reader.entries {
			if entry.sourceID == result.view.FocusID {
				reader.jumpReadingEntry(index, p.input.Value())
				break
			}
		}
	}
	return reader, nil
}

func (m model) openCurrentReading() model {
	if len(m.entries) == 0 {
		return m
	}
	reader := newModel(nil, nil)
	reader.width, reader.height = m.width, m.height
	reader.currentModel, reader.workingDirectory = m.currentModel, m.workingDirectory
	reader.entries = append([]transcriptEntry(nil), m.entries...)
	reader.processGroups = append([]processGroup(nil), m.processGroups...)
	reader.reading = &sessionReading{previous: &m}
	reader.input.Blur()
	reader.resizeLayout()
	reader.refreshViewport(true)
	reader.openTurnDirectory()
	return reader
}

func (m *model) jumpReadingEntry(index int, query string) {
	if index < 0 || index >= len(m.entries) {
		return
	}
	entry := m.entries[index]
	if entry.kind == entryAssistant && !entry.conclusion {
		for i := range m.processGroups {
			if m.processGroups[i].id == entry.processID {
				m.processGroups[i].collapsed = false
			}
		}
	}
	m.viewport.setItems(m.transcriptItems())
	for i, item := range m.viewport.items {
		if item.key/16 == index && item.key >= 0 && item.key%16 <= int(transcriptConclusion) {
			m.viewport.index, m.viewport.part, m.viewport.line = i, 0, item.gap
			if query = strings.ToLower(strings.TrimSpace(query)); query != "" {
				for part, piece := range m.viewport.itemParts(i) {
					if piece.source != "" && !strings.Contains(strings.ToLower(piece.source), query) {
						continue
					}
					m.viewport.part, m.viewport.line = part, piece.gap
					for row, line := range m.viewport.partLines(i, part) {
						if strings.Contains(strings.ToLower(ansi.Strip(line)), query) {
							m.viewport.line = piece.gap + max(0, row-2)
							break
						}
					}
					break
				}
			}
			m.selection.clear()
			return
		}
	}
}

func (m *model) openTurnDirectory() {
	r := m.reading
	r.turns = nil
	for index, entry := range m.entries {
		if entry.kind == entryUser {
			r.turns = append(r.turns, index)
		}
	}
	r.savedViewport = m.viewport
	r.directory = true
	r.selected = max(len(r.turns)-1, 0)
	m.showTurnDirectory()
}

func (m *model) showTurnDirectory() {
	r := m.reading
	items := make([]transcriptItem, 0, len(r.turns))
	for i, index := range r.turns {
		prefix := "  "
		if i == r.selected {
			prefix = "› "
		}
		question := sanitizeToolDetail(strings.Join(strings.Fields(m.entries[index].text), " "), false)
		line := fmt.Sprintf("%s%d  %s", prefix, i+1, question)
		items = append(items, staticTranscriptItem(i, ansi.Truncate(line, m.viewport.Width(), "…")))
	}
	if len(items) == 0 {
		items = append(items, staticTranscriptItem(-1, "No user questions in this view"))
	}
	m.viewport.setItems(items)
	if len(items) <= m.viewport.Height() {
		m.viewport.GotoTop()
	}
	if r.selected < m.viewport.index {
		m.viewport.index = r.selected
	}
	if r.selected >= m.viewport.index+m.viewport.Height() {
		m.viewport.index = r.selected - m.viewport.Height() + 1
	}
	m.viewport.part, m.viewport.line = 0, 0
}

func (m model) closeReading() model {
	previous := *m.reading.previous
	previous.width, previous.height = m.width, m.height
	previous.resizeLayout()
	previous.resizeSessionPicker()
	previous.refreshViewport(false)
	return previous
}

func (m model) handleReadingKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.reading
	m.selection.clear()
	if key.String() == "ctrl+d" {
		return m, tea.Quit
	}
	if r.directory {
		switch key.String() {
		case "esc", "t", "ctrl+t":
			r.directory = false
			m.viewport = r.savedViewport
			m.resizeLayout()
			m.refreshViewport(false)
		case "up", "k":
			r.selected = max(0, r.selected-1)
			m.showTurnDirectory()
		case "down", "j":
			r.selected = min(max(0, len(r.turns)-1), r.selected+1)
			m.showTurnDirectory()
		case "pgup":
			r.selected = max(0, r.selected-m.viewport.Height())
			m.showTurnDirectory()
		case "pgdown":
			r.selected = min(max(0, len(r.turns)-1), r.selected+m.viewport.Height())
			m.showTurnDirectory()
		case "enter":
			if len(r.turns) > 0 {
				r.directory = false
				m.viewport = r.savedViewport
				m.resizeLayout()
				m.refreshViewport(false)
				m.jumpReadingEntry(r.turns[r.selected], "")
			}
		case "ctrl+c":
			return m.closeReading(), nil
		}
		return m, nil
	}
	switch key.String() {
	case "esc", "ctrl+c":
		previous := m.closeReading()
		if previous.sessionPicker != nil {
			command := previous.requestSessionSearch()
			return previous, command
		}
		return previous, nil
	case "t", "ctrl+t":
		m.openTurnDirectory()
	case "up", "k":
		m.viewport.scroll(-1)
	case "down", "j":
		m.viewport.scroll(1)
	case "pgup":
		m.viewport.PageUp()
	case "pgdown", " ":
		m.viewport.PageDown()
	case "home":
		m.viewport.GotoTop()
	case "end":
		if r.latest != nil {
			m.replaceTranscript(r.latest)
			r.otherBranch = false
		}
		m.viewport.GotoBottom()
	case "enter":
		previous := m.closeReading()
		if previous.sessionPicker != nil {
			return previous.resumeSelectedSession()
		}
		return previous, nil
	}
	return m, nil
}
