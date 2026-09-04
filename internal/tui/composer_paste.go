package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

const (
	// inputPasteLineThreshold collapses a paste that adds at least this many
	// newlines at once: such a jump never fits the visible composer and is
	// a paste, not typing.
	inputPasteLineThreshold = inputMaximumHeight
	// inputPasteCharThreshold collapses a single update that adds this many
	// runes at once, catching one-line pastes that would wrap past the
	// viewport.
	inputPasteCharThreshold = 1000
	// pasteExcerptRunes keeps the placeholder chip short enough to stay on
	// one visual line.
	pasteExcerptRunes = 5
)

// Placeholder delimiters are square brackets with a ·lines suffix shape
// (e.g. [excerpt·200lines]) so tokens read like ordinary text. Unknown
// bracket spans stay plain text; only tokens recorded in model.pastes
// expand.
const (
	pasteTokenOpen  = '['
	pasteTokenClose = ']'
)

// pasteAttachment remembers one collapsed long paste. The textarea holds only
// the short token inline; submit and history paths expand tokens back to the
// full text in order.
type pasteAttachment struct {
	token string
	text  string
	lines int
}

// exceedsPasteThreshold reports whether freshly added text is large enough to
// collapse into a placeholder.
func exceedsPasteThreshold(value string) bool {
	if strings.Count(value, "\n") >= inputPasteLineThreshold {
		return true
	}
	return utf8.RuneCountInString(value) > inputPasteCharThreshold
}

// pasteLineCount counts logical lines without opening a phantom line for a
// trailing newline: "a\n" is one line, not two.
func pasteLineCount(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n") + 1
	if strings.HasSuffix(text, "\n") {
		count--
	}
	return count
}

// pasteExcerpt takes the first line of pasted text for the chip label.
// Whitespace folds to · so the token never contains spaces and word-wise
// cursor motion already treats it as one word.
func pasteExcerpt(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	joined := strings.Join(strings.Fields(first), "·")
	runes := []rune(joined)
	if len(runes) > pasteExcerptRunes {
		runes = runes[:pasteExcerptRunes]
	}
	return string(runes)
}

// pasteTokenLabel renders one placeholder chip, e.g. [你好世界真·200lines].
func pasteTokenLabel(excerpt string, lines int) string {
	if excerpt == "" {
		excerpt = "paste"
	}
	return fmt.Sprintf("%c%s·%dlines%c", pasteTokenOpen, excerpt, lines, pasteTokenClose)
}

// newPasteAttachment records pasted text and mints a unique token for it.
// Duplicate labels gain a numeric suffix so every token maps to exactly one
// attachment.
func (m *model) newPasteAttachment(text string) pasteAttachment {
	attachment := pasteAttachment{
		text:  text,
		lines: pasteLineCount(text),
	}
	base := pasteTokenLabel(pasteExcerpt(text), attachment.lines)
	token := base
	for index := 2; m.pasteTokenKnown(token); index++ {
		token = strings.TrimSuffix(base, string(pasteTokenClose)) +
			fmt.Sprintf("·%d%c", index, pasteTokenClose)
	}
	attachment.token = token
	return attachment
}

// pasteTokenKnown reports whether a token is already attached.
func (m model) pasteTokenKnown(token string) bool {
	for _, attachment := range m.pastes {
		if attachment.token == token {
			return true
		}
	}
	return false
}

// insertPastePlaceholder collapses pasted text into an inline token at the
// cursor, keeping surrounding text in place: AAAA[…]bbb.
func (m *model) insertPastePlaceholder(content string) {
	attachment := m.newPasteAttachment(content)
	m.pastes = append(m.pastes, attachment)
	m.input.InsertString(attachment.token)
	m.status = pastePlaceholderStatus(attachment.lines)
}

// expandComposerText replaces every attached token with its full text,
// preserving the order everything was pasted or typed in. Unknown bracket
// spans are left untouched as plain text.
func (m model) expandComposerText() string {
	value := m.input.Value()
	if len(m.pastes) == 0 {
		return value
	}
	ordered := make([]pasteAttachment, len(m.pastes))
	copy(ordered, m.pastes)
	sort.Slice(ordered, func(i, j int) bool {
		return len(ordered[i].token) > len(ordered[j].token)
	})
	for _, attachment := range ordered {
		if strings.Contains(value, attachment.token) {
			value = strings.ReplaceAll(value, attachment.token, attachment.text)
		}
	}
	return value
}

// dropOrphanPasteAttachments forgets attachments whose token no longer
// occurs in the composer, keeping the token set consistent with the text.
func (m *model) dropOrphanPasteAttachments() {
	if len(m.pastes) == 0 {
		return
	}
	value := m.input.Value()
	kept := make([]pasteAttachment, 0, len(m.pastes))
	for _, attachment := range m.pastes {
		if strings.Contains(value, attachment.token) {
			kept = append(kept, attachment)
		}
	}
	m.pastes = kept
}

// composerRows splits the composer value into logical rows for cursor math.
func (m model) composerRows() []string {
	return strings.Split(m.input.Value(), "\n")
}

// pasteTokenSpansInRow returns [start, end) rune spans of attached tokens on
// one logical row. Tokens never contain newlines, so every span stays on a
// single row.
func (m model) pasteTokenSpansInRow(row int) [][2]int {
	rows := m.composerRows()
	if row < 0 || row >= len(rows) || len(m.pastes) == 0 {
		return nil
	}
	runes := []rune(rows[row])
	var spans [][2]int
	for _, attachment := range m.pastes {
		token := []rune(attachment.token)
		if len(token) == 0 || len(token) > len(runes) {
			continue
		}
		for start := 0; start+len(token) <= len(runes); start++ {
			if runes[start] != pasteTokenOpen {
				continue
			}
			match := true
			for offset, r := range token {
				if runes[start+offset] != r {
					match = false
					break
				}
			}
			if match {
				spans = append(spans, [2]int{start, start + len(token)})
			}
		}
	}
	return spans
}

// pasteTokenBoundary classifies the cursor against the token spans of its
// row: at the start of, strictly inside, or at the end of a token.
func pasteTokenBoundary(spans [][2]int, col int) (start, end int, atStart, inside, atEnd bool) {
	for _, span := range spans {
		switch {
		case col == span[0]:
			return span[0], span[1], true, false, false
		case col > span[0] && col < span[1]:
			return span[0], span[1], false, true, false
		case col == span[1]:
			return span[0], span[1], false, false, true
		}
	}
	return 0, 0, false, false, false
}

// deletePasteTokenForward removes the token starting at start on the current
// row through the textarea's own deletion, leaving the cursor where the
// token began. Same-row forward deletes never cross a line boundary.
func (m *model) deletePasteTokenForward(start, end int) tea.Cmd {
	m.input.SetCursorColumn(start)
	var commands []tea.Cmd
	for range end - start {
		var command tea.Cmd
		m.input, command = m.input.Update(forwardDeleteKeyMsg())
		commands = append(commands, command)
	}
	m.dropOrphanPasteAttachments()
	m.status = "Removed pasted placeholder"
	return tea.Batch(commands...)
}

func forwardDeleteKeyMsg() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete})
}

// handlePasteTokenKey keeps placeholder tokens atomic for cursor motion and
// deletion. It returns handled=false for keys the textarea should process
// normally; updateInput's post-pass still corrects stray landings inside a
// token and drops orphaned attachments.
func (m model) handlePasteTokenKey(message tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if len(m.pastes) == 0 {
		return m, nil, false
	}
	row, col := m.input.Line(), m.input.Column()
	start, end, atStart, inside, atEnd := pasteTokenBoundary(m.pasteTokenSpansInRow(row), col)
	switch {
	case key.Matches(message, m.input.KeyMap.CharacterBackward):
		if atEnd {
			m.input.SetCursorColumn(start)
			return m, nil, true
		}
	case key.Matches(message, m.input.KeyMap.CharacterForward):
		if atStart {
			m.input.SetCursorColumn(end)
			return m, nil, true
		}
	case key.Matches(message, m.input.KeyMap.DeleteCharacterBackward):
		if atEnd || inside {
			return m, m.deletePasteTokenForward(start, end), true
		}
	case key.Matches(message, m.input.KeyMap.DeleteCharacterForward):
		if atStart || inside {
			return m, m.deletePasteTokenForward(start, end), true
		}
	case key.Matches(message, m.input.KeyMap.DeleteWordBackward):
		if atEnd {
			return m, m.deletePasteTokenForward(start, end), true
		}
	case key.Matches(message, m.input.KeyMap.DeleteWordForward):
		if atStart {
			return m, m.deletePasteTokenForward(start, end), true
		}
	}
	return m, nil, false
}

// snapCursorOutOfPasteToken pushes a cursor that landed strictly inside a
// token to its nearest edge after word-wise or vertical motion. Same-row
// corrections follow the travel direction; cross-row landings take the
// nearer edge.
func (m *model) snapCursorOutOfPasteToken(previousRow, previousCol int) {
	if len(m.pastes) == 0 {
		return
	}
	row, col := m.input.Line(), m.input.Column()
	start, end, _, inside, _ := pasteTokenBoundary(m.pasteTokenSpansInRow(row), col)
	if !inside {
		return
	}
	if row == previousRow {
		if col > previousCol {
			m.input.SetCursorColumn(end)
		} else {
			m.input.SetCursorColumn(start)
		}
		return
	}
	if col-start < end-col {
		m.input.SetCursorColumn(start)
	} else {
		m.input.SetCursorColumn(end)
	}
}

// splitInputChange diffs one composer update into the text before the change,
// the added text, and the text after it. Rune-based so boundaries never split
// a multibyte character.
func splitInputChange(previous, next string) (before, added, after string) {
	prevRunes := []rune(previous)
	nextRunes := []rune(next)
	start := 0
	for start < len(prevRunes) && start < len(nextRunes) &&
		prevRunes[start] == nextRunes[start] {
		start++
	}
	endPrevious, endNext := 0, 0
	for endPrevious < len(prevRunes)-start && endNext < len(nextRunes)-start &&
		prevRunes[len(prevRunes)-1-endPrevious] == nextRunes[len(nextRunes)-1-endNext] {
		endPrevious++
		endNext++
	}
	return string(nextRunes[:start]),
		string(nextRunes[start : len(nextRunes)-endNext]),
		string(nextRunes[len(nextRunes)-endNext:])
}

// collapseLargeInsert folds a just-landed large insert (clipboard paste and
// other non-PasteMsg paths) into a placeholder in place, leaving the cursor
// right after the token.
func (m *model) collapseLargeInsert(before, added, after string) tea.Cmd {
	attachment := m.newPasteAttachment(added)
	m.pastes = append(m.pastes, attachment)
	m.input.SetValue(before + attachment.token + after)
	// SetValue parks the cursor at the end; step back over the tail so the
	// cursor rests right after the token. Backward steps are logical line
	// moves, unaffected by soft wrapping.
	var commands []tea.Cmd
	for range []rune(after) {
		var command tea.Cmd
		m.input, command = m.input.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
		commands = append(commands, command)
	}
	m.status = pastePlaceholderStatus(attachment.lines)
	return tea.Batch(commands...)
}

// editorFinishedMsg arrives after the external editor exits.
type editorFinishedMsg struct {
	file   string
	editor string
	err    error
}

// defaultComposerEditor resolves $VISUAL, then $EDITOR, then vi. Arguments
// are supported (e.g. EDITOR="code --wait") by splitting on whitespace;
// no shell is involved.
func defaultComposerEditor() string {
	if editor := strings.TrimSpace(os.Getenv("VISUAL")); editor != "" {
		return editor
	}
	if editor := strings.TrimSpace(os.Getenv("EDITOR")); editor != "" {
		return editor
	}
	return "vi"
}

// pastePlaceholderStatus names the resolved editor so the first long paste
// teaches the Ctrl+G target without a separate configuration step.
func pastePlaceholderStatus(lines int) string {
	return fmt.Sprintf(
		"Pasted %d lines as placeholder; Ctrl+G edits in %s, Backspace removes as one",
		lines,
		defaultComposerEditor(),
	)
}

// openComposerEditor hands the expanded composer text to the user's default
// editor. The program pauses while the editor runs; on exit the edited file
// refills the composer as plain text without placeholders.
func (m model) openComposerEditor() (model, tea.Cmd) {
	editor := defaultComposerEditor()
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		m.status = "editor unavailable: set VISUAL or EDITOR to an installed editor"
		return m, nil
	}
	if _, err := exec.LookPath(fields[0]); err != nil {
		m.status = fmt.Sprintf(
			"editor %q not found; set VISUAL or EDITOR to an installed editor",
			fields[0],
		)
		return m, nil
	}
	file, err := os.CreateTemp("", "aice-compose-*.md")
	if err != nil {
		m.status = "editor unavailable: could not create a temp file"
		return m, nil
	}
	if _, err := file.WriteString(m.expandComposerText()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		m.status = "editor unavailable: could not write the draft"
		return m, nil
	}
	_ = file.Close()
	command := exec.Command(fields[0], append(fields[1:], file.Name())...)
	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		return editorFinishedMsg{file: file.Name(), editor: editor, err: err}
	})
}

// applyEditorResult refills the composer with the edited file as plain text.
// Placeholders are intentionally gone afterwards: the edited result scrolls
// normally instead of collapsing again.
func (m model) applyEditorResult(message editorFinishedMsg) model {
	defer func() { _ = os.Remove(message.file) }()
	if message.err != nil {
		if message.editor != "" {
			m.status = fmt.Sprintf("editor %q exited with an error; draft kept", message.editor)
		} else {
			m.status = "editor exited with an error; draft kept"
		}
		return m
	}
	data, err := os.ReadFile(message.file)
	if err != nil {
		m.status = "editor result unreadable; draft kept"
		return m
	}
	// Never wipe the draft on an empty result: GUI editors that exit without
	// blocking (e.g. missing --wait) can come back before anything is saved.
	// Clearing stays an explicit Backspace in the composer.
	if strings.TrimSpace(string(data)) == "" {
		m.status = "editor returned empty result; draft kept"
		return m
	}
	m.pastes = nil
	m.input.SetValue(strings.TrimRight(string(data), "\r\n"))
	m.input.CursorEnd()
	m.historyIndex = -1
	m.historyDraft = ""
	m.commandSelection = 0
	m.commandDismissed = false
	m.status = "Edited in external editor; scroll to review"
	m.resizeLayout()
	return m
}

// highlightPasteTokens tints attached placeholders inside the rendered input
// without changing their visible width, so the terminal cursor stays aligned.
func (m model) highlightPasteTokens(view string) string {
	for _, attachment := range m.pastes {
		if strings.Contains(view, attachment.token) {
			view = strings.ReplaceAll(view, attachment.token, infoStyle.Render(attachment.token))
		}
	}
	return view
}
