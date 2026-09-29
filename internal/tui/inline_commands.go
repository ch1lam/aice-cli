package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// slashAtCursor targets one whitespace-delimited token on any logical line.
// Paths, URLs, code spans and confirmed attachments are not command queries.
func (m model) slashAtCursor() (start, end int, query string, ok bool) {
	runes := []rune(m.input.editableReferenceText())
	cursor := m.input.cursorOffset()
	if cursor > len(runes) {
		return
	}
	start = cursor
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if start == len(runes) || runes[start] != '/' {
		return
	}
	end = cursor
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	if cursor <= start {
		return
	}
	query = string(runes[start:cursor])
	if strings.ContainsAny(string(runes[start+1:end]), "/`\"'") {
		return start, end, "", false
	}
	// A slash after whitespace within backticks is still literal code.
	if insideCommandCodeSpan(runes[:start]) {
		return start, end, "", false
	}
	rowStart := cursor - m.input.Column()
	for _, span := range m.pasteTokenSpansInRow(m.input.Line()) {
		if start < rowStart+span[1] && end > rowStart+span[0] {
			return start, end, "", false
		}
	}
	return start, end, query, true
}

// Track matching backtick runs, including double-backtick spans and fences.
func insideCommandCodeSpan(prefix []rune) bool {
	delimiter := 0
	for i := 0; i < len(prefix); {
		if prefix[i] == '\\' && delimiter == 0 {
			i += 2
			continue
		}
		if prefix[i] != '`' {
			i++
			continue
		}
		end := i + 1
		for end < len(prefix) && prefix[end] == '`' {
			end++
		}
		count := end - i
		if delimiter == 0 {
			delimiter = count
		} else if count == delimiter {
			delimiter = 0
		}
		i = end
	}
	return delimiter != 0
}

func (m model) slashHasSurroundingDraft(start, end int) bool {
	runes := []rune(m.input.Value())
	return strings.TrimSpace(string(runes[:start])+string(runes[end:])) != "" ||
		len(m.pastes) > 0 || len(m.input.files) > 0 || len(m.input.skills) > 0
}

// Inline commands temporarily borrow the composer for their existing argument,
// menu and credential flows. The saved draft owns every attachment and cursor.
func (m model) chooseSlashCommand() (model, tea.Cmd, bool) {
	command, exists := m.selectedSlashCommand()
	start, end, _, ok := m.slashAtCursor()
	if !exists || !ok {
		return m, nil, true
	}
	if command.SkillName != "" {
		m.attachSkill(start, end, command.SkillName)
		return m.settleCommand(false, nil)
	}
	if m.slashHasSurroundingDraft(start, end) {
		m.input.replaceRange(start, end, "")
		draft := m.captureComposerDraft()
		m.commandDraft = &draft
		m.input.SetValue("/" + command.Name)
		m.pastes = nil
		next, cmd, handled := m.submitSlashCommand("/"+command.Name, SlashCommandRequest{Name: command.Name})
		next.restoreCommandDraftIfIdle()
		next.resizeLayout()
		return next, cmd, handled
	}
	m.completeSelectedSlashCommand()
	return m.settleCommand(false, nil)
}

func (m *model) restoreCommandDraftIfIdle() {
	if m.commandDraft == nil || m.running || m.inputContext().domain != inputMain {
		return
	}
	draft := *m.commandDraft
	m.commandDraft = nil
	m.restoreComposerDraft(draft)
	m.commandSelection = 0
	m.commandDismissed = true
	m.resizeLayout()
}

// Typed leading skill invocations with arguments still attach on submit. Only
// the raw draft is inspected, never expanded paste/file content.
func (m *model) attachLeadingSkill() {
	value := strings.TrimLeftFunc(m.input.Value(), unicode.IsSpace)
	request, ok := parseSlashCommand(value)
	if !ok {
		return
	}
	command, exists := findSlashCommand(m.commands, request.Name)
	if !exists || command.SkillName == "" {
		return
	}
	start := utf8.RuneCountInString(m.input.Value()) - utf8.RuneCountInString(value)
	m.attachSkill(start, start+utf8.RuneCountInString("/"+request.Name), command.SkillName)
}
