package tui

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// A skill is bound to a range, so an identical label in pasted text grants no
// attachment. Ordinary edits shift intact ranges and discard intersected ones.
type composerSkill struct {
	start, end int
	name       string
}

func (m *composerInput) rebaseSkills(start, oldEnd, newEnd int) {
	kept := make([]composerSkill, 0, len(m.skills))
	for _, skill := range m.skills {
		switch {
		case skill.end <= start:
			kept = append(kept, skill)
		case skill.start >= oldEnd:
			skill.start += newEnd - oldEnd
			skill.end += newEnd - oldEnd
			kept = append(kept, skill)
		}
	}
	m.skills = kept
}

func (m *composerInput) setCursorOffset(offset int) {
	// Textarea SetValue leaves the cursor at the end. Move through its public
	// API to preserve scroll/soft-wrap state, including across logical lines.
	focused := m.Model.Focused()
	if !focused {
		m.Model.Focus()
		defer m.Model.Blur()
	}
	m.Model.MoveToEnd()
	for m.cursorOffset() > offset {
		before := m.cursorOffset()
		m.Model, _ = m.Model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
		if m.cursorOffset() == before {
			break
		}
	}
}

func (m *composerInput) replaceRange(start, end int, text string) {
	before := m.Value()
	runes := []rune(before)
	m.Model.SetValue(string(runes[:start]) + text + string(runes[end:]))
	m.rebaseFiles(before, start)
	m.setCursorOffset(start + utf8.RuneCountInString(text))
}

func (m *model) attachSkill(start, end int, name string) {
	label := "[skill:" + sanitizeSingleLineText(name) + "]"
	replacement := label
	runes := []rune(m.input.Value())
	if end == len(runes) || !unicode.IsSpace(runes[end]) {
		replacement += " "
	}
	m.input.replaceRange(start, end, replacement)
	m.input.skills = append(slices.Clone(m.input.skills), composerSkill{
		start: start, end: start + utf8.RuneCountInString(label), name: name,
	})
	slices.SortFunc(m.input.skills, func(a, b composerSkill) int { return a.start - b.start })
	m.commandSelection = 0
	m.commandDismissed = false
}

func (m model) composerSkills() []string {
	var names []string
	for _, skill := range m.input.skills {
		if !slices.Contains(names, skill.name) {
			names = append(names, skill.name)
		}
	}
	return names
}

func (m model) captureComposerDraft() composerDraft {
	return composerDraft{text: m.input.Value(), cursor: m.input.cursorOffset(),
		pastes: slices.Clone(m.pastes), files: slices.Clone(m.input.files), skills: slices.Clone(m.input.skills)}
}

func (m *model) restoreComposerDraft(draft composerDraft) {
	m.input.SetValue(draft.text)
	m.input.files = slices.Clone(draft.files)
	m.input.skills = slices.Clone(draft.skills)
	m.pastes = slices.Clone(draft.pastes)
	m.input.setCursorOffset(draft.cursor)
}

// External editing can move existing labels. Rebind only unambiguous surviving
// labels that were already attached; newly typed labels remain literal text.
func (m *composerInput) restoreEditedSkills(previous string, skills []composerSkill) {
	for _, skill := range skills {
		label := string([]rune(previous)[skill.start:skill.end])
		if strings.Count(previous, label) != 1 || strings.Count(m.Value(), label) != 1 {
			continue
		}
		start := utf8.RuneCountInString(m.Value()[:strings.Index(m.Value(), label)])
		m.skills = append(m.skills, composerSkill{start: start, end: start + utf8.RuneCountInString(label), name: skill.name})
	}
	slices.SortFunc(m.skills, func(a, b composerSkill) int { return a.start - b.start })
}
